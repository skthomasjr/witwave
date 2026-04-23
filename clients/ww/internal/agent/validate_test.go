package agent

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantErr string // substring; empty means no error expected
	}{
		{"happy lowercase", "hello", ""},
		{"happy with hyphen", "my-agent-1", ""},
		{"happy single char", "a", ""},
		{"happy max length", strings.Repeat("a", maxAgentNameLen), ""},

		{"empty", "", "must not be empty"},
		{"uppercase", "Hello", "DNS-1123"},
		{"underscore", "hello_world", "DNS-1123"},
		{"leading hyphen", "-hello", "DNS-1123"},
		{"trailing hyphen", "hello-", "DNS-1123"},
		{"dot", "hello.world", "DNS-1123"},
		{"space", "hello world", "DNS-1123"},
		{"over limit", strings.Repeat("a", maxAgentNameLen+1), "maximum is"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateName(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateName(%q) = %v; want nil", tc.input, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateName(%q) = nil; want error containing %q", tc.input, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateName(%q) error = %q; want substring %q", tc.input, err, tc.wantErr)
			}
		})
	}
}
