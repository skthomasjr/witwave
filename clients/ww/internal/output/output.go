// Package output centralises human/JSON formatting, colors, and tty
// detection so every command renders consistently.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"golang.org/x/term"
	"sigs.k8s.io/yaml"
)

// Mode is the output format for a single invocation.
type Mode int

const (
	// ModeHuman renders rich, colored, tabular text for a terminal.
	ModeHuman Mode = iota
	// ModeJSON renders pretty-printed JSON for snapshot commands and
	// line-delimited JSON for stream commands.
	ModeJSON
	// ModeJSONCompact renders single-line JSON (still line-delimited
	// for streams).
	ModeJSONCompact
	// ModeYAML renders YAML for snapshot commands (#1707). Stream
	// commands keep emitting NDJSON regardless — there is no
	// "streaming YAML" parity equivalent worth implementing.
	ModeYAML
)

// Writer bundles stdout / stderr with the chosen mode.
type Writer struct {
	Out   io.Writer
	Err   io.Writer
	Mode  Mode
	Color bool
}

// New configures output based on --json/--compact/--yaml, NO_COLOR, and TTY
// detection on stdout. yamlMode wins when both --json and --yaml are set —
// the caller is expected to validate flag-mutex upstream and we choose YAML
// here as a safe default rather than refusing to render.
func New(stdout, stderr io.Writer, jsonMode, compact, yamlMode bool) *Writer {
	mode := ModeHuman
	if yamlMode {
		mode = ModeYAML
	} else if jsonMode {
		mode = ModeJSON
		if compact {
			mode = ModeJSONCompact
		}
	}
	useColor := colorEnabled(stdout)
	if !useColor {
		color.NoColor = true
	}
	return &Writer{Out: stdout, Err: stderr, Mode: mode, Color: useColor}
}

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsJSON reports whether the writer emits JSON output.
func (w *Writer) IsJSON() bool {
	return w.Mode == ModeJSON || w.Mode == ModeJSONCompact
}

// IsYAML reports whether the writer emits YAML output (#1707).
func (w *Writer) IsYAML() bool {
	return w.Mode == ModeYAML
}

// EmitYAML writes v as YAML using sigs.k8s.io/yaml (the same JSON-
// compatible serializer kubectl + helm use, so field names + types
// match -o json output exactly except for the surrounding shape).
func (w *Writer) EmitYAML(v any) error {
	buf, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Out.Write(buf)
	return err
}

// EmitJSON writes v as JSON. In ModeJSON it pretty-prints; in
// ModeJSONCompact it's a single line. Snapshot callers use this; stream
// callers should call EmitJSONLine per event.
func (w *Writer) EmitJSON(v any) error {
	var (
		buf []byte
		err error
	)
	if w.Mode == ModeJSONCompact {
		buf, err = json.Marshal(v)
	} else {
		buf, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	_, err = w.Out.Write(buf)
	return err
}

// EmitJSONLine writes v as a single-line JSON record followed by \n.
// Used for streaming mode regardless of compact flag.
func (w *Writer) EmitJSONLine(v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	_, err = w.Out.Write(buf)
	return err
}

// EmitRaw writes s to stdout.
func (w *Writer) EmitRaw(s string) {
	_, _ = io.WriteString(w.Out, s)
}

// Errorf prints a red error prefix to stderr and returns an exit-code
// hint (1 = logical). Callers that hit a transport error should exit 2.
func (w *Writer) Errorf(format string, a ...any) {
	red := color.New(color.FgRed, color.Bold).SprintFunc()
	fmt.Fprint(w.Err, red("error: "))
	// Render first, then decide on newline from the RENDERED text.
	// Checking the format string instead ignored any trailing newline
	// supplied through a `%s`-formatted argument and still emitted an
	// extra blank line. (#1243)
	rendered := fmt.Sprintf(format, a...)
	fmt.Fprint(w.Err, rendered)
	if !strings.HasSuffix(rendered, "\n") {
		fmt.Fprintln(w.Err)
	}
}

// Warnf prints a yellow warning to stderr.
func (w *Writer) Warnf(format string, a ...any) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprint(w.Err, yellow("warn: "))
	rendered := fmt.Sprintf(format, a...)
	fmt.Fprint(w.Err, rendered)
	if !strings.HasSuffix(rendered, "\n") {
		fmt.Fprintln(w.Err)
	}
}

// Headerf writes a bold header line to stdout.
func (w *Writer) Headerf(format string, a ...any) {
	bold := color.New(color.Bold).SprintfFunc()
	rendered := bold(format, a...)
	fmt.Fprint(w.Out, rendered)
	if !strings.HasSuffix(rendered, "\n") {
		fmt.Fprintln(w.Out)
	}
}
