// Package output centralises human/JSON formatting, colors, and tty
// detection so every command renders consistently.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"golang.org/x/term"
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
)

// Writer bundles stdout / stderr with the chosen mode.
type Writer struct {
	Out   io.Writer
	Err   io.Writer
	Mode  Mode
	Color bool
}

// New configures output based on --json/--compact, NO_COLOR, and TTY
// detection on stdout.
func New(stdout, stderr io.Writer, jsonMode, compact bool) *Writer {
	mode := ModeHuman
	if jsonMode {
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
	fmt.Fprintf(w.Err, format, a...)
	if len(format) == 0 || format[len(format)-1] != '\n' {
		fmt.Fprintln(w.Err)
	}
}

// Warnf prints a yellow warning to stderr.
func (w *Writer) Warnf(format string, a ...any) {
	yellow := color.New(color.FgYellow).SprintFunc()
	fmt.Fprint(w.Err, yellow("warn: "))
	fmt.Fprintf(w.Err, format, a...)
	if len(format) == 0 || format[len(format)-1] != '\n' {
		fmt.Fprintln(w.Err)
	}
}

// Headerf writes a bold header line to stdout.
func (w *Writer) Headerf(format string, a ...any) {
	bold := color.New(color.Bold).SprintfFunc()
	fmt.Fprint(w.Out, bold(format, a...))
	if len(format) == 0 || format[len(format)-1] != '\n' {
		fmt.Fprintln(w.Out)
	}
}
