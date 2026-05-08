package conversation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StreamEnvelope mirrors shared/session_stream.py's SessionStreamEnvelope
// so we can decode the SSE `data:` payload directly. Field shape comes
// from `to_dict()` in that file.
type StreamEnvelope struct {
	Type    string                 `json:"type"`
	Version int                    `json:"version"`
	ID      string                 `json:"id"`
	TS      string                 `json:"ts"`
	AgentID string                 `json:"agent_id"`
	Payload map[string]interface{} `json:"payload"`
}

// StreamSession opens an SSE connection to the backend's
// /api/sessions/<id>/stream endpoint and pushes every envelope onto the
// returned channel. The channel closes when the stream ends (server
// disconnect, server-side overrun event, or ctx cancel). Errors during
// stream setup come back synchronously; once the goroutine starts, any
// mid-stream error closes the channel and is logged via the optional
// onError callback.
//
// `streamBaseURL` is what the port-forward helper hands back when called
// with portforward.BackendHTTPPort (port 8001 — the backend container's
// app listener, NOT the harness). The harness has no per-session SSE
// endpoint; only the backend does, because the backend owns the live
// conversation log for in-flight sessions.
func StreamSession(
	ctx context.Context,
	streamBaseURL, token, sessionID string,
	onError func(error),
) (<-chan StreamEnvelope, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/stream", streamBaseURL, sessionID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// No timeout on the HTTP client — SSE connections are long-lived
	// by design. The context above is the cancellation surface.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open SSE stream: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// happy path — fall through to streaming loop
	case http.StatusUnauthorized:
		resp.Body.Close()
		return nil, fmt.Errorf("unauthorized: backend rejected the bearer token (set CONVERSATIONS_AUTH_TOKEN on the backend env, or set CONVERSATIONS_AUTH_DISABLED=true for local dev)")
	case http.StatusServiceUnavailable:
		resp.Body.Close()
		return nil, fmt.Errorf("session stream not configured on the backend: CONVERSATIONS_AUTH_TOKEN unset and CONVERSATIONS_AUTH_DISABLED not set; the endpoint fails closed by design")
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("session %s not found on the backend", sessionID)
	default:
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("session stream: HTTP %d (%.200s)", resp.StatusCode, string(body))
	}

	out := make(chan StreamEnvelope, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// SSE frames can be larger than the default 64KiB on a long
		// conversation entry — bump to 1MiB to match harness/log scanners.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var (
			currentEvent string
			currentID    string
			currentData  strings.Builder
		)
		flushFrame := func() {
			defer func() {
				currentEvent = ""
				currentID = ""
				currentData.Reset()
			}()
			data := currentData.String()
			if data == "" || currentEvent == "" {
				return
			}
			var env StreamEnvelope
			if err := json.Unmarshal([]byte(data), &env); err != nil {
				if onError != nil {
					onError(fmt.Errorf("decode envelope: %w (data: %.200s)", err, data))
				}
				return
			}
			// Stamp the event type onto the envelope if the SSE line
			// said so but the JSON payload didn't include it (defensive).
			if env.Type == "" {
				env.Type = currentEvent
			}
			if env.ID == "" {
				env.ID = currentID
			}
			select {
			case out <- env:
			case <-ctx.Done():
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				// blank line terminates an SSE frame
				flushFrame()
				continue
			}
			if strings.HasPrefix(line, ":") {
				// SSE comment (keepalive) — ignore
				continue
			}
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				currentEvent = strings.TrimSpace(v)
				continue
			}
			if v, ok := strings.CutPrefix(line, "id: "); ok {
				currentID = strings.TrimSpace(v)
				continue
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				if currentData.Len() > 0 {
					currentData.WriteByte('\n')
				}
				currentData.WriteString(v)
				continue
			}
		}
		// Final flush in case the connection ended mid-frame
		flushFrame()

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			if onError != nil {
				onError(fmt.Errorf("read stream: %w", err))
			}
		}
	}()

	return out, nil
}

// FormatTS turns the ISO-8601-with-tz timestamps the harness emits
// (`2026-05-08T04:24:52.814162+00:00`) into the compact UTC form the
// CLI uses (`2026-05-08 04:24:52`). UTC is the only timezone the harness
// uses, so the `+00:00` suffix is consistently noise; microseconds are
// almost never useful in human-readable CLI output.
//
// On parse failure, returns the input unchanged so we never silently
// drop a value the user might still find diagnostic.
func FormatTS(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try the wider ISO-8601 form harness sometimes emits with
		// `+00:00` rather than `Z`. RFC3339Nano accepts both shapes,
		// so this is the rare malformed-string path; pass through.
		return ts
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

// FormatTSCompact returns just the time portion (HH:MM:SS) when caller
// has confirmed every timestamp is same-day, otherwise full date+time.
// Used by the table renderer so a single-day session list doesn't
// repeat the date on every row.
func FormatTSCompact(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("15:04:05")
}
