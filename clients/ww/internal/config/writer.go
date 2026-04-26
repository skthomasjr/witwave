package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Writer is a handle for reading + modifying a single config file on
// disk. Values are staged in memory (via an internal *viper.Viper)
// until Save writes them back. Save preserves other keys the user
// already had in the file — it's a merge-update, not a rewrite from
// scratch.
//
// Known limitation: comments and whitespace in the source file are
// NOT preserved across a Save. Viper decodes the file through
// mapstructure and re-encodes from the parsed tree; comments live
// only in the original tokens. If a user hand-authors a config.toml
// with inline documentation and then runs `ww config set`, the saved
// file will lose those comments. Acceptable trade-off for the MVP:
// our documented config surface is small, users who annotate
// heavily can edit the file directly.
type Writer struct {
	v       *viper.Viper
	path    string
	existed bool
	// dirty tracks whether any Set call changed state since the last
	// Save. Used so `ww config set` can error early when the caller
	// did nothing actionable.
	dirty bool
}

// OpenWriter resolves the target file (same precedence as Load's file
// search) and returns a Writer ready to mutate it. The file is created
// on the first Save if it doesn't exist; the parent directory is
// created too.
//
// cfgPath wins when non-empty (matches --config flag semantics). When
// both cfgPath and WW_CONFIG are empty and no default search path
// file exists, the Writer targets $HOME/.witwave/config.toml (the
// primary default).
func OpenWriter(cfgPath string, getenv func(string) string) (*Writer, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if cfgPath == "" {
		cfgPath = getenv("WW_CONFIG")
	}
	if cfgPath == "" {
		// Prefer the first existing file from the default search
		// paths, so `ww config set` modifies the config that Load
		// would currently use. If nothing exists yet, fall back to
		// the brand-aligned primary default.
		cfgPath = findExistingConfig(getenv)
	}
	if cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil, errors.New("cannot determine user home dir for config target")
		}
		cfgPath = filepath.Join(home, ".witwave", "config.toml")
	}

	v := viper.New()
	v.SetConfigType("toml")
	v.SetConfigFile(cfgPath)

	existed := true
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		existed = false
	} else if err != nil {
		return nil, fmt.Errorf("stat %s: %w", cfgPath, err)
	}

	if existed {
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read %s: %w", cfgPath, err)
		}
	}

	return &Writer{
		v:       v,
		path:    cfgPath,
		existed: existed,
	}, nil
}

// Path returns the absolute path of the config file this Writer
// targets. Useful for the `ww config path` verb.
func (w *Writer) Path() string { return w.path }

// Existed returns true if the file was present on disk when the
// Writer was opened. Callers use this to decide whether a `set` is
// creating a brand new file (and therefore should chmod it 0600 for
// the bearer-token-contains case).
func (w *Writer) Existed() bool { return w.existed }

// Get returns the current value of key as a string, or "" if unset.
// Keys use dot notation (e.g. "update.mode", "profile.default.token").
func (w *Writer) Get(key string) string {
	if !w.v.IsSet(key) {
		return ""
	}
	return w.v.GetString(key)
}

// AllSettings returns every key currently staged in the writer as a
// flat map. Used by `ww config list` (future) and for debugging.
func (w *Writer) AllSettings() map[string]any {
	return w.v.AllSettings()
}

// Set validates key and value against the allowlist and stages the
// change. Call Save to persist. Unknown keys or invalid values return
// an error WITHOUT mutating state; a failed Set is a no-op.
func (w *Writer) Set(key, value string) error {
	validated, err := validateSet(key, value)
	if err != nil {
		return err
	}
	w.v.Set(key, validated)
	w.dirty = true
	return nil
}

// SetTUICreateDefaults writes the `[tui.create_defaults]` block in
// one shot. Bypasses validateSet (which is for user-typed
// `ww config set` input); the TUI is in-process and the values
// were already validated by the form's submit path.
//
// Stages all seven keys atomically — partial-write isn't a useful
// state for these. Save() persists the staged change.
func (w *Writer) SetTUICreateDefaults(d TUICreateDefaults) {
	w.v.Set("tui.create_defaults.namespace", d.Namespace)
	w.v.Set("tui.create_defaults.backend", d.Backend)
	w.v.Set("tui.create_defaults.team", d.Team)
	w.v.Set("tui.create_defaults.create_namespace", d.CreateNamespace)
	w.v.Set("tui.create_defaults.existing_secret", d.ExistingSecret)
	w.v.Set("tui.create_defaults.secrets", d.Secrets)
	w.v.Set("tui.create_defaults.gitops_repo", d.GitOpsRepo)
	w.dirty = true
}

// Unset removes a key from the config. Calling Unset on a key that
// isn't present is a no-op (no error). Save persists the removal.
func (w *Writer) Unset(key string) error {
	if _, err := validateKey(key); err != nil {
		return err
	}
	// Viper has no public "unset" helper. The documented workaround is
	// to write all settings minus the target to a fresh Viper, then
	// swap. For our usage (small maps), that's fine.
	all := w.v.AllSettings()
	deleteNested(all, strings.Split(key, "."))
	fresh := viper.New()
	fresh.SetConfigType("toml")
	fresh.SetConfigFile(w.path)
	for k, val := range all {
		fresh.Set(k, val)
	}
	w.v = fresh
	w.dirty = true
	return nil
}

// Save writes the staged state back to disk. If the file didn't exist
// when the Writer was opened, Save creates parent directories as
// needed and chmod 0600s the new file (bearer tokens live inside).
//
// #1607: enforces mode 0600 on every Save (not only first-write) and
// chmods the parent directory to 0700. Older installs created by a
// pre-fix ww may have landed at 0o644 / 0o755; rather than scolding
// the user, every write tightens the perms to the intended posture.
//
// #1654: writes via the atomic create-temp + chmod + rename pattern
// (mirroring update.writeCache). The previous approach pre-created
// the target inode at 0o600 with O_EXCL and then asked Viper to
// rewrite it in place — but the O_EXCL pre-create lost the race on
// any second-or-later Save (file already exists, fall through to
// Viper's umask-honouring os.Create, file briefly visible at 0o644
// before the post-Chmod tightened it). Render the rendered TOML to
// a temp file with mode 0o600, then atomically Rename onto the final
// path. The final inode is never observed at any mode but 0o600.
//
// Returns a no-op error when nothing was staged — the caller is
// probably confused about which key they were trying to write, and
// should surface that rather than silently writing an unchanged file.
func (w *Writer) Save() error {
	if !w.dirty {
		return errors.New("nothing to save — no keys were changed")
	}

	// Ensure the parent directory exists before Viper tries to write,
	// and tighten its perms to 0700 on every save (#1607). MkdirAll is
	// a no-op when the dir already exists at any mode, so we follow
	// with an explicit Chmod to handle pre-existing 0o755 directories
	// from older ww versions. Skip on Windows (ACL-based model).
	parent := filepath.Dir(w.path)
	if parent != "." && parent != "/" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", parent, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(parent, 0o700); err != nil {
				// Non-fatal — the file chmod below is the load-bearing
				// guarantee. Surface as a warning so an admin-owned
				// parent dir doesn't break `ww config set`.
				fmt.Fprintf(os.Stderr,
					"ww: warning: chmod 0700 %s failed: %v\n", parent, err)
			}
		}
	}

	// Render TOML to a buffer first so the temp-file write is a
	// single Write call we can attribute cleanly on failure.
	var buf bytes.Buffer
	if err := w.v.WriteConfigTo(&buf); err != nil {
		return fmt.Errorf("render %s: %w", w.path, err)
	}

	// Atomic write: CreateTemp lands at 0o600 on POSIX (documented
	// implementation choice; we pin with an explicit Chmod below to
	// stay safe across future stdlib changes). Rename swaps the temp
	// over the target without ever flashing the final path through a
	// 0o644 mode.
	tmp, err := os.CreateTemp(parent, ".ww-config-*.toml")
	if err != nil {
		// Fall back to the older write-then-chmod flow so a misbehaving
		// parent directory (e.g. nosuid filesystems that refuse temp
		// creation) doesn't break `ww config set` outright. Loud WARN
		// so the operator knows their write went through the legacy
		// path with a brief 0o644 window.
		fmt.Fprintf(os.Stderr,
			"ww: warning: atomic temp-file create in %s failed (%v); "+
				"falling back to direct write at %s — there will be a "+
				"brief window before chmod 0600 lands\n",
			parent, err, w.path)
		return w.saveLegacy()
	}
	tmpPath := tmp.Name()
	// Defensive cleanup: if Rename succeeds, this Remove targets a path
	// that no longer exists and is a harmless no-op.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	// Pin 0o600 explicitly before Rename so the final inode is never
	// exposed at any looser mode. CreateTemp's current 0o600 default
	// is implementation, not contract — see update.writeCache for the
	// same reasoning.
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("chmod %s: %w", tmpPath, err)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, w.path); err != nil {
		// Loud WARN so the operator can find the orphan temp under
		// the same parent dir if they need to recover the rendered
		// content; the deferred Remove will then clean it up.
		fmt.Fprintf(os.Stderr,
			"ww: warning: atomic rename %s -> %s failed: %v\n",
			tmpPath, w.path, err)
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, w.path, err)
	}
	w.existed = true
	return nil
}

// saveLegacy is the pre-#1654 write-then-chmod path, retained as a
// fallback for filesystems where os.CreateTemp in the parent dir is
// rejected (rare — typically nosuid/noexec mounts or read-only parents
// that briefly accept a write to a known filename via Viper's path).
// Carries the documented brief 0o644 window; the caller has already
// emitted a WARN before invoking this.
func (w *Writer) saveLegacy() error {
	if err := w.v.WriteConfig(); err != nil {
		return fmt.Errorf("write %s: %w", w.path, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(w.path, 0o600); err != nil {
			return fmt.Errorf("chmod %s: %w", w.path, err)
		}
	}
	w.existed = true
	return nil
}

// findExistingConfig walks the default search path order and returns
// the first existing config.toml. Empty string means "no file found" —
// callers fall back to the preferred default.
func findExistingConfig(getenv func(string) string) string {
	for _, dir := range defaultConfigPaths(getenv) {
		p := filepath.Join(dir, "config.toml")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// deleteNested walks a dotted key path into a nested map tree and
// removes the terminal key. Used by Unset to prune a value without
// disturbing sibling keys.
func deleteNested(m map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(m, path[0])
		return
	}
	child, ok := m[path[0]].(map[string]any)
	if !ok {
		return
	}
	deleteNested(child, path[1:])
	if len(child) == 0 {
		delete(m, path[0])
	}
}

// --- validation ------------------------------------------------------

// SettableKey describes one key the CLI's `ww config set` verb accepts.
// The typed validator returns the parsed value (so the Writer stores
// a native Go value rather than a string blob where possible).
type SettableKey struct {
	Key         string
	Description string
	Validate    func(string) (any, error)
}

// SettableKeys is the allowlist of keys `ww config set` accepts. Every
// addition here should come with a ParseX in the consuming package
// (e.g. update.ParseMode) so the shape is validated here before being
// written to disk. Prevents "ww config set update.mode wubwub" from
// silently persisting a value that would log-and-disable at runtime.
//
// Profile keys use a single "default" profile shape for now —
// multi-profile write support can grow here when the UX calls for it.
func SettableKeys() []SettableKey {
	return []SettableKey{
		{
			Key:         "update.mode",
			Description: "off | notify | prompt | auto",
			Validate: func(s string) (any, error) {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "off", "notify", "prompt", "auto":
					return strings.ToLower(strings.TrimSpace(s)), nil
				}
				return nil, fmt.Errorf("mode must be one of off/notify/prompt/auto, got %q", s)
			},
		},
		{
			Key:         "update.channel",
			Description: "stable | beta",
			Validate: func(s string) (any, error) {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "stable", "beta":
					return strings.ToLower(strings.TrimSpace(s)), nil
				}
				return nil, fmt.Errorf("channel must be stable or beta, got %q", s)
			},
		},
		{
			Key:         "update.interval",
			Description: "Go duration string (e.g. 24h, 7d, 1h)",
			Validate: func(s string) (any, error) {
				d, err := time.ParseDuration(strings.TrimSpace(s))
				if err != nil {
					return nil, fmt.Errorf("interval must be a duration, got %q: %w", s, err)
				}
				if d <= 0 {
					return nil, fmt.Errorf("interval must be positive, got %s", d)
				}
				return s, nil // store canonical string, parse at use-time
			},
		},
		{
			Key:         "profile.default.base_url",
			Description: "harness base URL",
			Validate: func(s string) (any, error) {
				u, err := url.Parse(strings.TrimSpace(s))
				if err != nil || u.Scheme == "" || u.Host == "" {
					return nil, fmt.Errorf("base_url must be a full URL (scheme://host), got %q", s)
				}
				if u.Scheme != "http" && u.Scheme != "https" {
					return nil, fmt.Errorf("base_url scheme must be http or https, got %q", u.Scheme)
				}
				return s, nil
			},
		},
		{
			Key:         "profile.default.token",
			Description: "bearer token for conversations/events endpoints",
			Validate:    nonEmptyString,
		},
		{
			Key:         "profile.default.run_token",
			Description: "bearer token for ad-hoc run endpoints",
			Validate:    nonEmptyString,
		},
		{
			Key:         "profile.default.timeout",
			Description: "per-request timeout (duration string)",
			Validate: func(s string) (any, error) {
				d, err := time.ParseDuration(strings.TrimSpace(s))
				if err != nil {
					return nil, fmt.Errorf("timeout must be a duration, got %q: %w", s, err)
				}
				if d <= 0 {
					return nil, fmt.Errorf("timeout must be positive, got %s", d)
				}
				return s, nil
			},
		},
	}
}

func validateKey(key string) (*SettableKey, error) {
	for _, sk := range SettableKeys() {
		if sk.Key == key {
			return &sk, nil
		}
	}
	return nil, fmt.Errorf("unknown config key %q — run `ww config list-keys` to see supported keys", key)
}

func validateSet(key, value string) (any, error) {
	sk, err := validateKey(key)
	if err != nil {
		return nil, err
	}
	return sk.Validate(value)
}

func nonEmptyString(s string) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("value must be non-empty")
	}
	return s, nil
}
