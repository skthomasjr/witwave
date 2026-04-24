package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// logsPageName is the Pages id used for the per-agent logs view.
// Single page; the controller swaps its contents when the user
// navigates between agents. A future multi-tab drill-down will add
// siblings ("agent-status", "agent-events") under the same page.
const logsPageName = "agent-logs"

// logsMaxLines caps how many lines the TextView retains before
// oldest-wins rotation. 5000 is enough for multi-hour tails at
// reasonable log rates; above that, `kubectl logs --since` is the
// right tool, not a scroll-up session.
const logsMaxLines = 5000

// agentLogsController owns the streaming log view for one selected
// agent. Created on-demand when the user hits Enter on the list;
// torn down on ESC back to the list page.
type agentLogsController struct {
	app    *tview.Application
	parent *agentListController

	agent     string
	namespace string

	// containers is the rotation order: always starts with "harness",
	// followed by each declared backend's name. The git-sync sidecar
	// is deliberately omitted — it logs noisily and has its own
	// kubectl observability path.
	containers []string

	// mu guards cycleIdx + cancel. Stream restarts on 'c' grab the
	// lock to swap both atomically so a double-press can't leave two
	// goroutines writing to the same TextView.
	mu        sync.Mutex
	cycleIdx  int
	cancel    context.CancelFunc
	lineCount int // consumed for autoscroll heuristics

	// UI primitives — the three-band layout mirrors the list page
	// so the eye doesn't have to re-learn the chrome on navigation.
	root   tview.Primitive
	header *tview.TextView
	body   *tview.TextView
	footer *tview.TextView
}

// openAgentLogs is what Enter on the list triggers. Derives the
// container rotation from the current snapshot (harness first, then
// declared backends) and pushes a fresh logs page into the Pages
// container. Subsequent opens reuse the same page — we rebuild the
// controller each time so nothing carries over from a previous
// agent's tail.
func (c *agentListController) openAgentLogs() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	row, _ := c.table.GetSelection()
	if row <= 0 || row-1 >= len(snap) {
		return
	}
	s := snap[row-1]

	containers := append([]string{"harness"}, s.Backends...)

	logs := &agentLogsController{
		app:        c.app,
		parent:     c,
		agent:      s.Name,
		namespace:  s.Namespace,
		containers: containers,
	}
	logs.buildUI()
	logs.start()

	// Replace any prior logs page so memory from a previous tail
	// doesn't leak. RemovePage is safe even if the page doesn't
	// exist yet.
	if c.pages.HasPage(logsPageName) {
		c.pages.RemovePage(logsPageName)
	}
	c.pages.AddPage(logsPageName, logs.root, true, true)
	c.app.SetFocus(logs.body)
}

// buildUI constructs the header / body / footer Flex layout.
// Header reflects the current container + stream status; body is the
// tailing TextView; footer lists keybindings. All three follow the
// same style vocabulary as the list's chrome.
func (l *agentLogsController) buildUI() {
	l.header = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false).
		SetWrap(false)

	l.body = tview.NewTextView().
		SetDynamicColors(false). // raw log lines — don't try to parse [colors]
		SetScrollable(true).
		SetWrap(false).
		SetMaxLines(logsMaxLines).
		SetChangedFunc(func() {
			// Autoscroll to the tail on every append. A future PR can
			// gate this on "user hasn't scrolled up" so manual scroll
			// pauses follow; for MVP always tail.
			l.app.Draw()
		})
	l.body.ScrollToEnd()

	l.footer = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetText("[#808080]c cycle container · esc back · q quit[-:-:-]")

	// Key handlers on the body: 'c' cycles containers; ESC returns to
	// the list. Ctrl-C / q still exit the app via the root handler.
	l.body.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			l.close()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'c':
				l.cycleContainer()
				return nil
			case 'q':
				// Let the app-level handler swallow this one —
				// consistent with how the list page quits. Returning
				// the event ensures the root InputCapture fires.
				return event
			}
		}
		return event
	})

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(l.header, 2, 0, false).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(l.body, 0, 1, true).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(l.footer, 1, 0, false)

	frame := tview.NewFrame(root).SetBorders(0, 0, 0, 0, 1, 1)
	frame.SetBorder(true).
		SetBorderColor(tcell.ColorDimGray).
		SetTitle(fmt.Sprintf(" ww tui · logs · %s/%s ", l.namespace, l.agent)).
		SetTitleColor(tcell.ColorSilver)
	l.root = frame
}

// start kicks off the log-tail goroutine against the first container
// in the rotation (harness). Called once after buildUI; cycleContainer
// handles subsequent swaps.
func (l *agentLogsController) start() {
	l.renderHeader("connecting…")
	l.streamCurrent()
}

// cycleContainer rotates to the next container in the list, cancels
// the running stream, clears the body, and kicks off a fresh tail.
// Wraps at the end so the rotation is infinite.
func (l *agentLogsController) cycleContainer() {
	l.mu.Lock()
	l.cycleIdx = (l.cycleIdx + 1) % len(l.containers)
	l.mu.Unlock()

	// Clear the body so the user isn't confusingly shown interleaved
	// lines from two containers during the swap. Context cancel +
	// fresh goroutine is handled by streamCurrent.
	l.body.Clear()
	l.streamCurrent()
}

// streamCurrent cancels the previous stream goroutine (if any) and
// starts a fresh one for the currently-selected container. Each
// stream has its own context so cancellation signals to agent.Logs
// that the caller walked away.
func (l *agentLogsController) streamCurrent() {
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
	}
	container := l.containers[l.cycleIdx]
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.lineCount = 0
	l.mu.Unlock()

	l.renderHeader(fmt.Sprintf("container=%s · tailing…", container))

	// agent.Logs writes to an io.Writer; the bridge writer queues
	// each write onto the UI thread via QueueUpdateDraw. Batching
	// per-write (not per-line) keeps the repaint cost proportional
	// to what the backend produces rather than to TextView updates.
	writer := &tviewBridgeWriter{
		app:   l.app,
		view:  l.body,
		onTap: func() { l.incLineCount() },
	}

	go func(ctx context.Context, container string) {
		err := agent.Logs(ctx, l.parent.cfg, agent.LogsOptions{
			Agent:     l.agent,
			Namespace: l.namespace,
			Container: container,
			Follow:    true,
			TailLines: 200,
			Out:       writer,
		})
		if ctx.Err() != nil {
			// Normal cancellation (container cycle or ESC) — no
			// status update; the new stream already owns the header.
			return
		}
		// Real error — surface in the header and stop. User can 'c'
		// to try a different container or ESC back to the list.
		msg := "stream ended"
		if err != nil {
			msg = "error: " + err.Error()
		}
		l.app.QueueUpdateDraw(func() {
			l.renderHeader(fmt.Sprintf("container=%s · %s", container, msg))
		})
	}(ctx, container)
}

// close tears the logs page down and returns focus to the list.
// Called on ESC. Cancels the running stream + removes the page so
// the next Enter on a different agent builds a clean controller.
func (l *agentLogsController) close() {
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.mu.Unlock()

	l.parent.pages.RemovePage(logsPageName)
	l.app.SetFocus(l.parent.table)
}

// renderHeader paints the two-line status strip. Line 1 identifies
// the agent; line 2 reflects the current stream status (container
// name, connection state, errors). Called from start/cycle/stream
// callbacks under QueueUpdateDraw when needed — but also fine off
// the main thread for simple text writes since tview.TextView's
// SetText is goroutine-safe.
func (l *agentLogsController) renderHeader(status string) {
	line1 := fmt.Sprintf(
		"[::b]%s/%s[-:-:-]  [#808080]containers: %s[-:-:-]",
		l.namespace, l.agent, strings.Join(l.containers, " / "),
	)
	line2 := fmt.Sprintf("[#d7af00]%s[-:-:-]", status)
	l.header.SetText(line1 + "\n" + line2)
}

// incLineCount bumps the line counter — used by the header's "N
// lines" indicator in a future polish pass. Kept as a hook now so
// the bridge writer doesn't need to know about header formatting.
func (l *agentLogsController) incLineCount() {
	l.mu.Lock()
	l.lineCount++
	l.mu.Unlock()
}

// ---------------------------------------------------------------------------
// tviewBridgeWriter — io.Writer → tview.TextView with UI-thread safety
// ---------------------------------------------------------------------------

// tviewBridgeWriter adapts an io.Writer call (from agent.Logs'
// background goroutine) into a safe TextView append on the UI
// thread. Each Write() queues a single QueueUpdateDraw — that's
// fine at log rates in the hundreds-of-lines-per-second range and
// simpler than a debounced batcher. If we hit a noisier source we
// can grow this into a ring buffer with a timer-driven flush.
type tviewBridgeWriter struct {
	app   *tview.Application
	view  *tview.TextView
	onTap func() // called once per Write; currently just bumps a counter
}

// Write implements io.Writer. agent.Logs' line writer calls this
// once per log line; we copy the slice (the scanner reuses its
// buffer) and queue the draw-time append.
func (w *tviewBridgeWriter) Write(p []byte) (int, error) {
	// Copy before crossing the goroutine boundary — the scanner
	// buffer backing `p` is reused on the next line; without the
	// copy, the UI thread could see stomped bytes by the time the
	// queued closure runs.
	chunk := make([]byte, len(p))
	copy(chunk, p)
	w.app.QueueUpdateDraw(func() {
		_, _ = w.view.Write(chunk)
	})
	if w.onTap != nil {
		w.onTap()
	}
	return len(p), nil
}

// Compile-time assertion that the bridge writer matches io.Writer —
// cheap, catches a drift in Write's signature before runtime.
var _ io.Writer = (*tviewBridgeWriter)(nil)
