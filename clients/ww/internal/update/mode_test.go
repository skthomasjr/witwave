package update

import "testing"

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
		ok   bool
	}{
		{"", ModeNotify, true},
		{"off", ModeOff, true},
		{"NOTIFY", ModeNotify, true},
		{"  prompt  ", ModePrompt, true},
		{"auto", ModeAuto, true},
		{"wubwub", "", false},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		gotOk := err == nil
		if gotOk != tc.ok {
			t.Errorf("ParseMode(%q) ok=%v, want %v (err=%v)", tc.in, gotOk, tc.ok, err)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestEffectiveMode_EnvOverrides(t *testing.T) {
	cases := []struct {
		name  string
		start Mode
		env   map[string]string
		tty   bool
		want  Mode
	}{
		{
			name:  "no overrides keeps configured",
			start: ModeNotify,
			env:   map[string]string{},
			tty:   true,
			want:  ModeNotify,
		},
		{
			name:  "WW_NO_UPDATE_CHECK forces off",
			start: ModeAuto,
			env:   map[string]string{"WW_NO_UPDATE_CHECK": "1"},
			tty:   true,
			want:  ModeOff,
		},
		{
			name:  "CI=true forces off",
			start: ModePrompt,
			env:   map[string]string{"CI": "true"},
			tty:   true,
			want:  ModeOff,
		},
		{
			name:  "CI=0 does not force off",
			start: ModeNotify,
			env:   map[string]string{"CI": "0"},
			tty:   true,
			want:  ModeNotify,
		},
		{
			name:  "GITHUB_ACTIONS forces off",
			start: ModeNotify,
			env:   map[string]string{"GITHUB_ACTIONS": "true"},
			tty:   true,
			want:  ModeOff,
		},
		{
			name:  "prompt auto-downgrades to notify without tty",
			start: ModePrompt,
			env:   map[string]string{},
			tty:   false,
			want:  ModeNotify,
		},
		{
			name:  "auto survives non-tty — user asked for unattended",
			start: ModeAuto,
			env:   map[string]string{},
			tty:   false,
			want:  ModeAuto,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			isTTY := func() bool { return tc.tty }
			got := EffectiveMode(tc.start, getenv, isTTY)
			if got != tc.want {
				t.Errorf("EffectiveMode(%v, env=%v, tty=%v) = %v, want %v",
					tc.start, tc.env, tc.tty, got, tc.want)
			}
		})
	}
}

func TestParseChannel(t *testing.T) {
	cases := []struct {
		in   string
		want Channel
		ok   bool
	}{
		{"", ChannelStable, true},
		{"stable", ChannelStable, true},
		{"BETA", ChannelBeta, true},
		{"  beta  ", ChannelBeta, true},
		{"nightly", "", false},
	}
	for _, tc := range cases {
		got, err := ParseChannel(tc.in)
		gotOk := err == nil
		if gotOk != tc.ok {
			t.Errorf("ParseChannel(%q) ok=%v, want %v (err=%v)", tc.in, gotOk, tc.ok, err)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("ParseChannel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
