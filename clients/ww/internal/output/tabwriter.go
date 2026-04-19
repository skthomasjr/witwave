package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Table prints headers + rows separated by tabs, aligned via
// text/tabwriter. Caller is responsible for consistent column counts.
func Table(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}

// KV prints key/value pairs aligned on ":". Used by `view` subcommands.
func KV(w io.Writer, pairs [][2]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(tw, "%s:\t%s\n", p[0], p[1])
	}
	_ = tw.Flush()
}
