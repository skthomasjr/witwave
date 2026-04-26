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

// aggregateContainerSentinel is the rotation entry that means
// "tail every container at once." Conceptually not a real container
// name — the controller reacts to it specially and spawns one tail
// per real container, each writing to the shared body with a
// `[<container>] ` prefix so interleaved output is distinguishable.
const aggregateContainerSentinel = "all"

// agentLogsController owns the streaming log view for one selected
// agent. Created on-demand when the user hits Enter on the list;
// torn down on ESC back to the list page.
type agentLogsController struct {
	app    *tview.Application
	parent *agentListController

	agent     string
	namespace string

	// containers is the rotation order. First entry is always the
	// `aggregateContainerSentinel` ("all") so the view opens with
	// every container fanned in — the default a user drilling down
	// from a degraded agent wants. Subsequent entries are "harness"
	// plus each declared backend; the git-sync sidecar is
	// deliberately omitted (logs noisily, its own kubectl path).
	containers []string

	// mu guards cycleIdx + cancel. Stream restarts on 'c' grab the
	// lock to swap both atomically so a double-press can't leave two
	// goroutines writing to the same TextView.
	mu        sync.Mutex
	cycleIdx  int
	cancel    context.CancelFunc
	lineCount int // consumed for autoscroll heuristics

	// wg tracks in-flight tailOne goroutines so cycleContainer/close
	// can wait for them to drain after cancelling ctx before starting
	// the next stream (or tearing the page down). Without this, a fast
	// 'c' press could leave the previous tail's goroutines running
	// against a stale ctx while the next streamCurrent has already
	// installed a new one — leaking goroutines and (more visibly)
	// kubectl/portforward connections that lsof would still see.
	// See witwave#1663.
	wg sync.WaitGroup

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

	// Rotation: aggregate first, then harness, then declared backends.
	containers := []string{aggregateContainerSentinel, "harness"}
	containers = append(containers, s.Backends...)

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
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.mu.Unlock()

	// Wait for prior tailOne goroutines to drain before starting the
	// next stream — otherwise aggregate-mode fan-out can leak kubectl
	// log connections across rapid cycles. agent.Logs returns promptly
	// once ctx is cancelled, so this is bounded in practice.
	// See witwave#1663.
	l.wg.Wait()

	// Clear the body so the user isn't confusingly shown interleaved
	// lines from two containers during the swap. Context cancel +
	// fresh goroutine is handled by streamCurrent.
	l.body.Clear()
	l.streamCurrent()
}

// streamCurrent cancels the previous stream goroutine(s) (if any)
// and starts fresh ones for the currently-selected rotation entry.
// Single-container mode spawns one tail; aggregate mode spawns one
// tail per real container and prefixes each line with
// `[<container>] ` so the interleaved output is distinguishable.
// Both share a single context so cancellation cleanly tears down
// all goroutines at once.
func (l *agentLogsController) streamCurrent() {
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
	}
	entry := l.containers[l.cycleIdx]
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.lineCount = 0
	l.mu.Unlock()

	// Shared bridge: agent.Logs' line writer → copy → TextView under
	// QueueUpdateDraw. Per-container prefix wrappers compose over it
	// in aggregate mode; single-container mode writes straight.
	bridge := &tviewBridgeWriter{
		app:   l.app,
		view:  l.body,
		onTap: func() { l.incLineCount() },
	}

	if entry == aggregateContainerSentinel {
		l.renderHeader("all containers · tailing…")
		// Aggregate: fan out one goroutine per real container (every
		// entry after the sentinel). Each writes through a prefixing
		// wrapper so an interleaved body like
		//     [harness] routing message to echo
		//     [echo] received prompt "ping"
		// is trivially readable. One shared ctx cancels them all.
		for _, container := range l.containers[1:] {
			prefixed := &prefixingWriter{
				inner:  bridge,
				prefix: []byte("[" + container + "] "),
			}
			// Add before spawning so cycleContainer/close can Wait()
			// reliably even if the goroutine hasn't started yet.
			// See witwave#1663.
			l.wg.Add(1)
			go l.tailOne(ctx, container, prefixed)
		}
		return
	}

	l.renderHeader(fmt.Sprintf("container=%s · tailing…", entry))
	// See witwave#1663.
	l.wg.Add(1)
	go l.tailOne(ctx, entry, bridge)
}

// tailOne is the single-container tail goroutine body. Factored out
// so aggregate mode can spawn many copies cheaply. Error surfacing
// goes through the header and is suppressed on normal ctx cancel
// (container cycle / ESC) so a rotation doesn't spam "stream ended"
// on every swap.
func (l *agentLogsController) tailOne(ctx context.Context, container string, out io.Writer) {
	// Pair with the wg.Add(1) in streamCurrent so cycleContainer/close
	// can Wait() for the goroutine pool to drain. See witwave#1663.
	defer l.wg.Done()
	err := agent.Logs(ctx, l.parent.cfg, agent.LogsOptions{
		Agent:     l.agent,
		Namespace: l.namespace,
		Container: container,
		Follow:    true,
		TailLines: 200,
		Out:       out,
	})
	if ctx.Err() != nil {
		return
	}
	msg := "stream ended"
	if err != nil {
		msg = "error: " + err.Error()
	}
	l.app.QueueUpdateDraw(func() {
		// In aggregate mode we stamp the error with the container
		// name so the user can tell which tail failed (the others
		// may still be healthy). Single-container mode re-renders
		// the canonical status line.
		if l.isAggregate() {
			l.renderHeader(fmt.Sprintf("all containers · %s: %s", container, msg))
		} else {
			l.renderHeader(fmt.Sprintf("container=%s · %s", container, msg))
		}
	})
}

// isAggregate reports whether the current rotation entry is the
// "all" sentinel. Guarded by the same mutex the cycle logic uses
// so a cycling user can't race this check into the wrong branch.
func (l *agentLogsController) isAggregate() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.containers[l.cycleIdx] == aggregateContainerSentinel
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

	// Drain in-flight tailOne goroutines so the page tear-down doesn't
	// race with their final QueueUpdateDraw on the about-to-be-removed
	// body, and so kubectl log connections close before the user sees
	// the list page again. See witwave#1663.
	l.wg.Wait()

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
	// Header lists the REAL containers (skip the aggregate sentinel —
	// it's a view mode, not a container).
	real := l.containers[1:]
	line1 := fmt.Sprintf(
		"[::b]%s/%s[-:-:-]  [#808080]containers: %s[-:-:-]",
		l.namespace, l.agent, strings.Join(real, " / "),
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

// ---------------------------------------------------------------------------
// prefixingWriter — per-container tag for aggregate-mode fan-in
// ---------------------------------------------------------------------------

// prefixingWriter is an io.Writer that prepends a fixed byte slice
// to every Write call before handing it to the inner writer. Used in
// aggregate mode so the multiple tail-goroutines' output is readable
// when interleaved in the body TextView (each line starts with
// `[<container>] `).
//
// Assumes each Write call represents one complete log line — which is
// true for agent.Logs' line-writer path (it calls the sink once per
// scanner.Text()). If that contract changes the prefix would start
// appearing mid-line; the assertion below guards the Writer
// interface shape against drift.
type prefixingWriter struct {
	inner  io.Writer
	prefix []byte
}

// Write composes the prefix with the incoming bytes into a single
// Write on the inner writer. Returns len(p) on success so callers
// see the "logical" count (they don't know about our prefix
// bytes). Errors short-circuit; the inner writer is the error
// surface.
func (w *prefixingWriter) Write(p []byte) (int, error) {
	combined := make([]byte, 0, len(w.prefix)+len(p))
	combined = append(combined, w.prefix...)
	combined = append(combined, p...)
	if _, err := w.inner.Write(combined); err != nil {
		return 0, err
	}
	return len(p), nil
}

var _ io.Writer = (*prefixingWriter)(nil)
