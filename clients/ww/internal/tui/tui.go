// Package tui implements the `ww tui` interactive surface (#1450).
//
// Current state: stub. One screen with a welcome banner + context
// confirmation + tracking-issue pointer. No cluster API calls; no
// feature panels yet. Establishes the tview framework that future
// panels (status, logs, events, session drill-down) will plug into.
//
// Framework choice: `rivo/tview` on `gdamore/tcell` — matches k9s,
// which is the UX reference for what `ww tui` will ultimately
// become (agent list → drill in → watch logs/events/sessions,
// vim-style navigation, slash-to-filter). Using the same framework
// means users carry over their k9s muscle memory for free;
// divergence would be a UX tax forever.
//
// Design notes:
//
//   - Single-screen stub today. The Flex-based composition +
//     modal-overlay patterns that the real panels will use are
//     already established in `Run()` so future PRs only add
//     pages to the existing Pages container.
//   - Graceful degradation. If kubeconfig resolution fails, the
//     TUI still launches and shows a "No cluster configured"
//     message in place of the context block. Never blocks on
//     missing config.
//   - Exit in three forms (q / esc / ctrl-c) all routed to
//     app.Stop(). No confirm dialog for a read-only surface.
package tui

import (
	"fmt"
	"strings"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Run starts the tview application with the given version +
// resolved context (or diagnostic string). Blocks until the user
// quits. Terminal state is restored automatically on exit via
// tview's own shutdown path.
func Run(version string, target *k8s.Target, contextErr string) error {
	app := tview.NewApplication()

	// Root layout uses a Pages container so future panels (status,
	// logs, events, session drill) can register as pages without
	// rearchitecting the entry point. The stub registers a single
	// "welcome" page today.
	pages := tview.NewPages()
	pages.AddPage("welcome", welcomePage(version, target, contextErr), true, true)

	// Global key bindings. Future per-page key handlers install
	// themselves on each primitive; the app-level handler only
	// owns actions that should work from every page (currently
	// just quit).
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC, tcell.KeyEscape:
			app.Stop()
			return nil
		case tcell.KeyRune:
			if event.Rune() == 'q' {
				app.Stop()
				return nil
			}
		}
		return event
	})

	return app.SetRoot(pages, true).EnableMouse(false).Run()
}

// welcomePage builds the stub's single page — header strip,
// centered welcome content, context block, footer strip. Returns
// a tview.Primitive so the caller can add it straight into a
// Pages container.
func welcomePage(version string, target *k8s.Target, contextErr string) tview.Primitive {
	// --- Header strip: "ww · TUI" left, version right ---
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(false).
		SetScrollable(false).
		SetTextAlign(tview.AlignLeft).
		SetText(fmt.Sprintf(
			"[::b][#00afff]ww · TUI[-:-:-]%s[#a0a0a0]%s[-:-:-]",
			strings.Repeat(" ", 60),
			version,
		))

	// --- Welcome body (centered) ---
	body := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(false).
		SetScrollable(false).
		SetTextAlign(tview.AlignCenter).
		SetWrap(true)
	body.SetText(buildBodyText(target, contextErr))

	// --- Footer strip: keybindings ---
	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[#808080]q · esc · ctrl-c  —  quit[-:-:-]")

	// --- Compose with a Flex in column mode ---
	//
	// Flex{ header (1) | body (auto-expand) | footer (1) }. tview
	// handles terminal resize; the body re-flows automatically.
	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(tview.NewBox(), 1, 0, false). // one-line gap under header
		AddItem(body, 0, 1, false).           // flex-expand to fill
		AddItem(tview.NewBox(), 1, 0, false). // one-line gap above footer
		AddItem(footer, 1, 0, false)

	// Wrap the root in a bordered Frame so the window reads as
	// one bounded workspace. The Frame will later carry status-
	// bar text at the top/bottom edges when real panels land.
	frame := tview.NewFrame(root).
		SetBorders(0, 0, 1, 1, 2, 2)
	frame.SetBorder(true).
		SetBorderColor(tcell.ColorDimGray).
		SetTitle(" ww tui ").
		SetTitleColor(tcell.ColorSilver)

	return frame
}

// buildBodyText assembles the centered welcome content including
// the "what's coming" bullets, the tracking-issue pointer, and
// the context block (or degraded message). Returns tview-tagged
// text so colours render without touching tcell directly.
func buildBodyText(target *k8s.Target, contextErr string) string {
	var b strings.Builder
	b.WriteString("\n\n\n")
	b.WriteString("[white::b]Welcome to ww[-:-:-]\n\n")
	b.WriteString("[#d0d0d0]The full interactive dashboard is on its way.[-:-:-]\n\n")
	b.WriteString("[#a0a0a0]  • Live operator status, logs, events[-:-:-]\n")
	b.WriteString("[#a0a0a0]  • Session drill-down with event stream[-:-:-]\n")
	b.WriteString("[#a0a0a0]  • Agent send + tail without leaving the terminal[-:-:-]\n\n")
	b.WriteString("[#d0d0d0]Follow along → [-:-:-][#00afff::u]https://github.com/skthomasjr/witwave/issues/1450[-:-:-]\n\n\n")

	// Context block OR degraded message.
	if target != nil && contextErr == "" {
		cluster := target.Cluster
		if cluster == "" {
			cluster = target.Server
		}
		if cluster == "" {
			cluster = "(unknown)"
		}
		ns := target.Namespace
		if ns == "" {
			ns = "default"
		}
		// Fixed-indent label/value rows so the block reads as a
		// coherent unit even on narrow terminals.
		b.WriteString(fmt.Sprintf("[#a0a0a0::b]Target cluster:[-:-:-]  [#d0d0d0]%s[-:-:-]\n", cluster))
		b.WriteString(fmt.Sprintf("[#a0a0a0::b]Context:[-:-:-]         [#d0d0d0]%s[-:-:-]\n", target.Context))
		b.WriteString(fmt.Sprintf("[#a0a0a0::b]Namespace:[-:-:-]       [#d0d0d0]%s[-:-:-]\n", ns))
	} else {
		msg := contextErr
		if msg == "" {
			msg = "No cluster configured — set $KUBECONFIG or pass --kubeconfig to ww tui."
		}
		b.WriteString("[#ffaf00]" + msg + "[-:-:-]\n")
	}

	return b.String()
}
