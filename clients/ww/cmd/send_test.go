// Tests for the pure-helper functions in send.go that the
// `ww send` subcommand composes over the A2A backend response.
// Mirrors the table-driven shape used in snapshot_test.go and
// validate_test.go; the HTTP path itself is exercised against a
// real backend, so this file just pins the response-flatten
// contract so a future tweak to the parts-payload shape fails
// loudly.
package cmd

import "testing"

// TestExtractText pins the A2A-response flatten helper used to render
// the agent's reply in the terminal after `ww send`. Contract:
//   - nil Result → empty string
//   - Result.Parts present → concatenate every "text"-kind part's text
//   - Result.Parts empty AND Result.Status.Message.Parts present →
//     concatenate every "text"-kind part's text from the status path
//   - non-text parts (kind != "text") are ignored
//   - parts list ordering is preserved in the output
//
// The dual-source behaviour (top-level Parts vs status-message Parts)
// reflects two A2A response shapes the spec allows; pin both branches
// so a future spec change forces a deliberate update here.
func TestExtractText(t *testing.T) {
	t.Run("nil result returns empty", func(t *testing.T) {
		r := a2aResponse{Result: nil}
		if got := extractText(r); got != "" {
			t.Errorf("extractText(nil-result) = %q, want empty", got)
		}
	})

	t.Run("top-level parts concatenated in order", func(t *testing.T) {
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{
			Parts: []a2aPart{
				{Kind: "text", Text: "hello "},
				{Kind: "text", Text: "world"},
			},
		}
		if got := extractText(r); got != "hello world" {
			t.Errorf("extractText = %q, want %q", got, "hello world")
		}
	})

	t.Run("non-text parts ignored", func(t *testing.T) {
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{
			Parts: []a2aPart{
				{Kind: "text", Text: "before "},
				{Kind: "image", Text: "should-not-appear"},
				{Kind: "text", Text: "after"},
			},
		}
		if got := extractText(r); got != "before after" {
			t.Errorf("extractText = %q, want %q", got, "before after")
		}
	})

	t.Run("empty top-level parts falls back to status-message parts", func(t *testing.T) {
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{
			Status: &struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			}{
				Message: &struct {
					Parts []a2aPart `json:"parts,omitempty"`
				}{
					Parts: []a2aPart{
						{Kind: "text", Text: "from-status"},
					},
				},
			},
		}
		if got := extractText(r); got != "from-status" {
			t.Errorf("extractText = %q, want %q", got, "from-status")
		}
	})

	t.Run("top-level parts win over status parts when both present", func(t *testing.T) {
		// If Parts is non-empty, the function never consults Status —
		// pin so a future "merge both" rewrite is a deliberate change.
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{
			Parts: []a2aPart{{Kind: "text", Text: "top"}},
			Status: &struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			}{
				Message: &struct {
					Parts []a2aPart `json:"parts,omitempty"`
				}{
					Parts: []a2aPart{{Kind: "text", Text: "status"}},
				},
			},
		}
		if got := extractText(r); got != "top" {
			t.Errorf("extractText = %q, want %q", got, "top")
		}
	})

	t.Run("empty top-level and nil status returns empty", func(t *testing.T) {
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{}
		if got := extractText(r); got != "" {
			t.Errorf("extractText = %q, want empty", got)
		}
	})

	t.Run("status with nil message returns empty", func(t *testing.T) {
		r := a2aResponse{}
		r.Result = &struct {
			Parts  []a2aPart `json:"parts,omitempty"`
			Status *struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			} `json:"status,omitempty"`
		}{
			Status: &struct {
				Message *struct {
					Parts []a2aPart `json:"parts,omitempty"`
				} `json:"message,omitempty"`
			}{Message: nil},
		}
		if got := extractText(r); got != "" {
			t.Errorf("extractText = %q, want empty", got)
		}
	})
}
