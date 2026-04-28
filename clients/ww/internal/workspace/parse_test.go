package workspace

import (
	"strings"
	"testing"
)

func TestParseVolumeSpecs_Shapes(t *testing.T) {
	cases := []struct {
		name    string
		input   []string
		want    []VolumeSpec
		wantErr string
	}{
		{
			name:  "name=size",
			input: []string{"source=50Gi"},
			want:  []VolumeSpec{{Name: "source", Size: "50Gi"}},
		},
		{
			name:  "name=size@class",
			input: []string{"source=50Gi@efs-sc"},
			want:  []VolumeSpec{{Name: "source", Size: "50Gi", StorageClassName: "efs-sc"}},
		},
		{
			name: "multiple",
			input: []string{
				"source=50Gi@efs-sc",
				"memory=10Gi",
			},
			want: []VolumeSpec{
				{Name: "source", Size: "50Gi", StorageClassName: "efs-sc"},
				{Name: "memory", Size: "10Gi"},
			},
		},
		{
			name:    "missing equals",
			input:   []string{"source50Gi"},
			wantErr: "expected `name=size",
		},
		{
			name:    "empty size",
			input:   []string{"source="},
			wantErr: "size after '=' is empty",
		},
		{
			name:    "empty class",
			input:   []string{"source=50Gi@"},
			wantErr: "storage class after '@' is empty",
		},
		{
			name:    "duplicate",
			input:   []string{"source=10Gi", "source=20Gi"},
			wantErr: `duplicate name "source"`,
		},
		{
			name:    "invalid name",
			input:   []string{"Source=10Gi"},
			wantErr: "must be lowercase",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVolumeSpecs(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q; got none", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d (got=%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %+v; want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseSecretSpecs_Shapes(t *testing.T) {
	cases := []struct {
		name    string
		input   []string
		want    []SecretSpec
		wantErr string
	}{
		{
			name:  "bare name",
			input: []string{"github-token"},
			want:  []SecretSpec{{Name: "github-token"}},
		},
		{
			name:  "envFrom",
			input: []string{"github-token=env"},
			want:  []SecretSpec{{Name: "github-token", EnvFrom: true}},
		},
		{
			name:  "mount path",
			input: []string{"docker-creds@/home/agent/.docker"},
			want:  []SecretSpec{{Name: "docker-creds", MountPath: "/home/agent/.docker"}},
		},
		{
			name:    "non-env mode after =",
			input:   []string{"x=mount"},
			wantErr: "only `=env` is recognised",
		},
		{
			name:    "relative mount",
			input:   []string{"x@home/foo"},
			wantErr: "must be absolute",
		},
		{
			name:    "empty",
			input:   []string{""},
			wantErr: "empty value",
		},
		{
			name:    "duplicate",
			input:   []string{"a", "a"},
			wantErr: `duplicate name "a"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSecretSpecs(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q; got none", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %+v; want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "ok", input: "witwave"},
		{name: "ok with hyphens", input: "my-workspace-1"},
		{name: "empty", input: "", wantErr: "must not be empty"},
		{name: "uppercase", input: "Wsp", wantErr: "DNS-1123 label"},
		{name: "underscore", input: "ws_1", wantErr: "DNS-1123 label"},
		{name: "starts with hyphen", input: "-ws", wantErr: "DNS-1123 label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v; want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseOutputFormat(t *testing.T) {
	cases := []struct {
		input   string
		want    OutputFormat
		wantErr bool
	}{
		{"", OutputFormatTable, false},
		{"table", OutputFormatTable, false},
		{"yaml", OutputFormatYAML, false},
		{"json", OutputFormatJSON, false},
		{"weird", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseOutputFormat(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error; got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}
