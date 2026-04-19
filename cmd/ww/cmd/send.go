package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type sendFlags struct {
	promptFile string
	backendID  string
	contextID  string
}

func newSendCmd() *cobra.Command {
	sf := &sendFlags{}
	cmd := &cobra.Command{
		Use:   "send <agent> [prompt]",
		Short: "Send an A2A prompt to an agent via the harness",
		Long: "Sends a message to the named agent through the harness A2A endpoint\n" +
			"(POST / with a JSON-RPC message/send payload). If no prompt is provided\n" +
			"inline, reads from --prompt-file or stdin.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)

			agent := args[0]
			prompt, err := readPrompt(args, sf.promptFile)
			if err != nil {
				return logicalErr(err)
			}
			prompt = strings.TrimSpace(prompt)
			if prompt == "" {
				return logicalErr(fmt.Errorf("empty prompt"))
			}

			// Resolve agent target: look up harness /agents directory,
			// but also accept a direct agent name used as a path segment
			// for harnesses that proxy /agents/<name>/ (dashboard does).
			// For a pure harness we POST to "/" and include a metadata
			// field so the harness can route; since /agents gives us the
			// harness card too, default to POST to base.
			targetURL, err := resolveSendURL(ctx, c, agent)
			if err != nil {
				return handleErr(out, err)
			}

			msgID := randomHex(8)
			ctxID := sf.contextID
			if ctxID == "" {
				ctxID = randomHex(8)
			}
			metadata := map[string]any{}
			if sf.backendID != "" {
				metadata["backend_id"] = sf.backendID
			}
			message := map[string]any{
				"messageId": msgID,
				"contextId": ctxID,
				"role":      "user",
				"parts":     []any{map[string]any{"kind": "text", "text": prompt}},
			}
			if len(metadata) > 0 {
				message["metadata"] = metadata
			}
			body := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "message/send",
				"params":  map[string]any{"message": message},
			}

			useRun := c.RunToken() != ""
			if !useRun && c.Token() == "" {
				out.Warnf("no run_token and no token configured; request will be unauthenticated")
			} else if !useRun {
				out.Warnf("no run_token; falling back to conversations token")
			}

			var resp a2aResponse
			if err := c.DoJSON(ctx, http.MethodPost, targetURL, body, &resp, useRun); err != nil {
				return handleErr(out, err)
			}
			if resp.Error != nil {
				out.Errorf("%s", resp.Error.Message)
				return logicalErr(fmt.Errorf("%s", resp.Error.Message))
			}
			if out.IsJSON() {
				return out.EmitJSON(resp)
			}
			text := extractText(resp)
			if text == "" {
				out.Warnf("empty response")
				return nil
			}
			fmt.Fprintln(out.Out, text)
			return nil
		},
	}
	cmd.Flags().StringVar(&sf.promptFile, "prompt-file", "", "read prompt text from this file (- for stdin)")
	cmd.Flags().StringVar(&sf.backendID, "backend", "", "route to a specific backend (claude|codex|gemini)")
	cmd.Flags().StringVar(&sf.contextID, "context", "", "reuse this A2A contextId (default: random)")
	return cmd
}

func resolveSendURL(ctx context.Context, c any, agent string) (string, error) {
	// A dashboard-style harness proxies /agents/<name>/ to the A2A root
	// of that agent's harness. Pure harness / single-agent deployments
	// accept POST "/" directly. We look up /agents; if we find a member
	// whose id matches, we return its absolute URL with a trailing
	// slash. Otherwise fall back to "/agents/<name>/" for dashboard
	// compatibility.
	type entry struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	var agents []entry
	hc, ok := c.(interface {
		DoJSON(ctx context.Context, method, path string, body, out any, useRun bool) error
	})
	if !ok {
		return "", fmt.Errorf("client type assertion failed")
	}
	if err := hc.DoJSON(ctx, http.MethodGet, "/agents", nil, &agents, false); err != nil {
		// Can't list agents — fall back to the dashboard-style proxy
		// path so send still works against cluster deployments.
		return "/agents/" + agent + "/", nil
	}
	for _, a := range agents {
		if a.ID == agent && a.URL != "" {
			u := strings.TrimRight(a.URL, "/")
			return u + "/", nil
		}
	}
	// Not in directory — still try the proxy path for forward-compat.
	return "/agents/" + agent + "/", nil
}

func readPrompt(args []string, file string) (string, error) {
	if len(args) >= 2 {
		return args[1], nil
	}
	if file == "" {
		return "", fmt.Errorf("no prompt provided: pass an argument or --prompt-file")
	}
	if file == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(file)
	return string(b), err
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback — deterministic but not unique; acceptable for
		// client-side ids.
		return "fallbackid"
	}
	return hex.EncodeToString(b)
}

type a2aError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Error   *a2aError `json:"error,omitempty"`
	Result  *struct {
		Parts  []a2aPart `json:"parts,omitempty"`
		Status *struct {
			Message *struct {
				Parts []a2aPart `json:"parts,omitempty"`
			} `json:"message,omitempty"`
		} `json:"status,omitempty"`
	} `json:"result,omitempty"`
}

func extractText(r a2aResponse) string {
	if r.Result == nil {
		return ""
	}
	parts := r.Result.Parts
	if len(parts) == 0 && r.Result.Status != nil && r.Result.Status.Message != nil {
		parts = r.Result.Status.Message.Parts
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
