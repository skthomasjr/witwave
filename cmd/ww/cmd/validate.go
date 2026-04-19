package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type validateFlags struct {
	kind string
}

func newValidateCmd() *cobra.Command {
	vf := &validateFlags{}
	cmd := &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a job/task/trigger/continuation/heartbeat markdown file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)

			path := args[0]
			body, err := os.ReadFile(path)
			if err != nil {
				return logicalErr(err)
			}
			kind := vf.kind
			if kind == "" {
				kind = inferKind(path)
			}
			if kind == "" {
				out.Warnf("could not infer kind from path; pass --kind to be explicit")
			}

			u, err := c.Resolve("/validate")
			if err != nil {
				return handleErr(out, err)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
			if err != nil {
				return handleErr(out, err)
			}
			req.Header.Set("Content-Type", "text/markdown")
			if kind != "" {
				req.Header.Set("X-Ww-Kind", kind)
				// Also carry as query param so servers that parse
				// query instead of header still work.
				q := req.URL.Query()
				q.Set("kind", kind)
				req.URL.RawQuery = q.Encode()
			}
			if tok := c.Token(); tok != "" {
				req.Header.Set("Authorization", "Bearer "+tok)
			} else if rt := c.RunToken(); rt != "" {
				req.Header.Set("Authorization", "Bearer "+rt)
			}
			req.Header.Set("User-Agent", "ww/"+Version)

			resp, err := c.HTTP().Do(req)
			if err != nil {
				out.Errorf("%v", err)
				return transportErr(err)
			}
			defer resp.Body.Close()

			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(resp.Body)
			if resp.StatusCode >= 500 {
				out.Errorf("harness returned %s: %s", resp.Status, buf.String())
				return transportErr(fmt.Errorf("%s", resp.Status))
			}
			if resp.StatusCode >= 400 {
				out.Errorf("%s: %s", resp.Status, buf.String())
				return logicalErr(fmt.Errorf("validation failed"))
			}
			// 2xx — harness accepted. The body may be JSON with
			// warnings / errors. Pass it through verbatim.
			if out.IsJSON() {
				out.EmitRaw(buf.String())
				if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
					out.EmitRaw("\n")
				}
			} else {
				if buf.Len() == 0 {
					fmt.Fprintln(out.Out, "OK")
				} else {
					fmt.Fprintln(out.Out, buf.String())
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&vf.kind, "kind", "", "file kind (job|task|trigger|continuation|heartbeat|webhook)")
	return cmd
}

func inferKind(path string) string {
	parent := strings.ToLower(filepath.Base(filepath.Dir(path)))
	switch parent {
	case "jobs":
		return "job"
	case "tasks":
		return "task"
	case "triggers":
		return "trigger"
	case "continuations":
		return "continuation"
	case "webhooks":
		return "webhook"
	}
	base := strings.ToLower(filepath.Base(path))
	if base == "heartbeat.md" {
		return "heartbeat"
	}
	return ""
}
