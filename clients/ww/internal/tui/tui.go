// Package tui implements the `ww tui` interactive surface (#1450).
//
// Current state: live agent list — polls the apiserver every 2
// seconds and renders WitwaveAgents across every namespace the
// caller can read. Matches `ww agent list`'s data model so future
// per-agent drill-down reuses the same `agent.AgentSummary` shape.
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
//   - Polling, not watch. 2-second interval, matches the k9s default.
//     Simpler than a long-lived watch.Interface with bookmark /
//     reconnect / 410-Gone handling, and at human-visible latencies
//     the savings don't matter. Upgrade to watch when we need
//     sub-second responsiveness or scale past the "handful of CRs"
//     regime.
//   - Snapshot-swap rendering. Each tick replaces the full table
//     contents. Selection survives the swap because the code
//     re-applies the selection by (namespace, name) identity, not by
//     row index.
//   - Graceful degradation. If kubeconfig resolution fails the TUI
//     still launches and shows the "No cluster configured" panel in
//     place of the table. Never blocks on missing config.
//   - Exit in three forms (q / esc / ctrl-c) all routed to
//     app.Stop(). No confirm dialog for a read-only surface.
//   - Drill-down (Enter on a row) is a stub in this PR — prints
//     a toast pointing at the tracking issue. The Pages container
//     is wired for real per-agent views in a follow-up.
package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/client-go/rest"
)

// pollInterval is how often the TUI re-lists agents from the
// apiserver. 2s feels instant to humans and matches k9s. Lower has
// diminishing returns + hits the apiserver harder.
const pollInterval = 2 * time.Second

// Run starts the tview application. Blocks until the user quits.
// Terminal state is restored automatically on exit via tview's own
// shutdown path.
//
// When cfg is nil (kubeconfig resolution failed) the TUI renders the
// degraded "No cluster configured" panel without starting a poll.
func Run(version string, target *k8s.Target, cfg *rest.Config, contextErr string) error {
	app := tview.NewApplication()
	pages := tview.NewPages()

	// Global quit bindings work from every page. Page-specific
	// handlers install themselves on the primitives they own.
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

	// Degraded mode — nothing to poll. Show the context-err panel and
	// let the user exit cleanly.
	if cfg == nil {
		pages.AddPage("no-cluster", degradedPanel(version, contextErr), true, true)
		return app.SetRoot(pages, true).Run()
	}

	// Live-list mode. Compose the three-band layout (header / table /
	// footer), wire polling + key handlers, start the goroutine.
	page, ctrl := newAgentListPage(app, version, target, cfg)
	ctrl.pages = pages
	pages.AddPage("agents", page, true, true)

	// Create-agent modal lives as a second page, added hidden. The
	// 'a' keybinding on the list toggles it visible (ShowPage) and
	// focused; submit/cancel call HidePage to return to the list.
	pages.AddPage("create-agent", newCreateAgentModal(ctrl), true, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ctrl.poll(ctx)

	return app.SetRoot(pages, true).EnableMouse(false).Run()
}

// ---------------------------------------------------------------------------
// Agent-list page
// ---------------------------------------------------------------------------

// agentListController owns the mutable state behind the live list:
// the latest snapshot, the fetch-in-progress flag, and the tview
// primitives that need to re-render when new data lands.
type agentListController struct {
	app     *tview.Application
	version string
	target  *k8s.Target
	cfg     *rest.Config

	// pages is the root Pages container — used by the list to show
	// modal overlays (create-agent form, future per-agent drill-down)
	// and by those modals to return focus to the list.
	pages *tview.Pages

	// mu guards snapshot + lastErr + lastFetch + fetching. Accessed
	// from the poll goroutine and from UI callbacks (refresh on 'r').
	mu         sync.Mutex
	snapshot   []agent.AgentSummary
	lastErr    error
	lastFetch  time.Time
	fetching   bool
	refreshNow chan struct{}

	// UI primitives — updated under app.QueueUpdateDraw from the
	// poll goroutine.
	header *tview.TextView
	table  *tview.Table
	footer *tview.TextView

	// createAgentForm is the modal bundled form + state + error
	// view. Built once in Run(); reset()ed on each open via 'a'.
	createAgentForm *createAgentForm
}

// newAgentListPage builds the Flex-composed live-list view + the
// controller that drives it. Returns both so Run() can hand the
// primitive to Pages and start the poll goroutine on the controller.
func newAgentListPage(app *tview.Application, version string, target *k8s.Target, cfg *rest.Config) (tview.Primitive, *agentListController) {
	ctrl := &agentListController{
		app:        app,
		version:    version,
		target:     target,
		cfg:        cfg,
		refreshNow: make(chan struct{}, 1),
	}

	ctrl.header = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(false).
		SetWrap(false)

	ctrl.table = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0) // keep the header row pinned while scrolling
	ctrl.table.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorDarkCyan).
		Foreground(tcell.ColorBlack))

	ctrl.footer = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(false)

	// Paint initial "loading" state so the user doesn't see a blank
	// screen before the first fetch lands. The header rollup updates
	// on the first snapshot; this is the pre-snapshot placeholder.
	ctrl.renderHeader()
	ctrl.renderFooter()
	ctrl.renderEmpty("[#d0d0d0]Loading agents…[-:-:-]")

	// Key handlers on the table — 'r' to force a refresh, 'a' to open
	// the create-agent modal, Enter is a stub until the per-agent
	// drill-down view lands.
	ctrl.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			ctrl.showDrillDownStub()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'r':
				// Non-blocking nudge to the poll goroutine. Dropping
				// when the channel's full is fine — a refresh is
				// already pending.
				select {
				case ctrl.refreshNow <- struct{}{}:
				default:
				}
				return nil
			case 'a':
				ctrl.openCreateAgent()
				return nil
			}
		}
		return event
	})

	// Flex composition: header (2 rows) | 1-row gap | table (fill) |
	// 1-row gap | footer (1 row). The gaps are empty Box primitives —
	// tview treats them as unclaimed vertical space so the table gets
	// clear visual breathing room from both the status strip above
	// and the keybinding hints below.
	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(ctrl.header, 2, 0, false).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(ctrl.table, 0, 1, true).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(ctrl.footer, 1, 0, false)

	frame := tview.NewFrame(root).SetBorders(0, 0, 0, 0, 1, 1)
	frame.SetBorder(true).
		SetBorderColor(tcell.ColorDimGray).
		SetTitle(" ww tui · agents ").
		SetTitleColor(tcell.ColorSilver)

	return frame, ctrl
}

// poll runs the 2-second fetch loop. Cancel the ctx to stop; the
// loop exits on the next tick. Also listens on refreshNow so the
// 'r' key fires an immediate fetch without waiting for the next tick.
func (c *agentListController) poll(ctx context.Context) {
	c.fetchAndRender(ctx)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.fetchAndRender(ctx)
		case <-c.refreshNow:
			c.fetchAndRender(ctx)
		}
	}
}

// fetchAndRender issues one ListAgents call and queues a repaint.
// Errors are surfaced in the header ("last: 5s ago — timed out")
// rather than interrupting the UI; stale data is better than a
// black screen when the apiserver blips.
func (c *agentListController) fetchAndRender(ctx context.Context) {
	c.mu.Lock()
	c.fetching = true
	c.mu.Unlock()

	// Bounded fetch — keep one missed tick from wedging the loop if
	// the apiserver stalls indefinitely. 5s is ~2.5x the pollInterval
	// so a transient slow call doesn't misfire as an error.
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	summaries, err := agent.ListAgents(fctx, c.cfg, agent.ListOptions{
		AllNamespaces: true,
	})

	c.mu.Lock()
	c.snapshot = summaries
	c.lastErr = err
	c.lastFetch = time.Now()
	c.fetching = false
	c.mu.Unlock()

	c.app.QueueUpdateDraw(func() {
		c.renderHeader()
		c.renderTable()
	})
}

// renderHeader writes the two-line status strip. Line 1 is cluster /
// context / last-update; line 2 is the phase rollup. Kept tight so
// it stays readable on narrow terminals.
func (c *agentListController) renderHeader() {
	c.mu.Lock()
	snap := c.snapshot
	err := c.lastErr
	last := c.lastFetch
	fetching := c.fetching
	c.mu.Unlock()

	// Line 1: cluster + context + freshness.
	cluster := "(no cluster)"
	ctx := "-"
	if c.target != nil {
		if c.target.Cluster != "" {
			cluster = c.target.Cluster
		} else if c.target.Server != "" {
			cluster = c.target.Server
		}
		if c.target.Context != "" {
			ctx = c.target.Context
		}
	}

	freshness := "—"
	if !last.IsZero() {
		age := time.Since(last).Round(time.Second)
		freshness = fmt.Sprintf("last: %s ago", formatShortDuration(age))
	}
	if fetching {
		freshness = "refreshing…"
	}
	freshnessColor := "[#808080]"
	if err != nil {
		freshnessColor = "[#ffaf00]"
	}

	line1 := fmt.Sprintf(
		"[::b]ww[-:-:-]  [#a0a0a0]cluster[-:-:-] %s  [#a0a0a0]ctx[-:-:-] %s  %s%s[-:-:-]  [#808080]· ww %s[-:-:-]",
		cluster, ctx, freshnessColor, freshness, c.version,
	)

	// Line 2: agent count rollup. Zero agents shows "0 total" — the
	// table renders its own empty-state help separately.
	var ready, degraded, pending, other int
	for _, s := range snap {
		switch s.Phase {
		case "Ready":
			ready++
		case "Degraded":
			degraded++
		case "Pending":
			pending++
		default:
			other++
		}
	}
	line2 := fmt.Sprintf(
		"[#a0a0a0]Agents:[-:-:-] %d total  •  [#008000]Ready %d[-:-:-]  •  [#ff5f00]Degraded %d[-:-:-]  •  [#d7af00]Pending %d[-:-:-]",
		len(snap), ready, degraded, pending,
	)
	if other > 0 {
		line2 += fmt.Sprintf("  •  [#808080]Other %d[-:-:-]", other)
	}
	if err != nil {
		line2 = fmt.Sprintf(
			"[#ff5f00]Fetch failed:[-:-:-] %s  [#808080](showing last snapshot)[-:-:-]",
			err.Error(),
		)
	}

	c.header.SetText(line1 + "\n" + line2)
}

// renderTable writes the snapshot into the tview.Table. Selection is
// preserved across redraws by (namespace, name) identity — so the
// highlighted row stays on the same agent even if another row was
// added above it between polls.
func (c *agentListController) renderTable() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	// Capture the currently-selected (namespace, name) BEFORE we
	// clear the table so we can restore it after the rebuild.
	selNamespace, selName := "", ""
	row, _ := c.table.GetSelection()
	if row > 0 && row-1 < len(snap) {
		selNamespace = snap[row-1].Namespace
		selName = snap[row-1].Name
	}

	c.table.Clear()

	// Header row. Dim-gray + bold so it reads as a label strip.
	headers := []string{"NAMESPACE", "TEAM", "NAME", "PHASE", "READY", "BACKENDS", "AGE"}
	for i, h := range headers {
		cell := tview.NewTableCell(h).
			SetTextColor(tcell.ColorSilver).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false).
			SetExpansion(1)
		c.table.SetCell(0, i, cell)
	}

	if len(snap) == 0 {
		c.renderEmpty("[#d0d0d0]No WitwaveAgents found.[-:-:-]\n\n" +
			"[#808080]Try:[-:-:-]\n" +
			"  [#00afff]ww agent create hello --create-namespace --backend echo[-:-:-]")
		return
	}

	// Reset any prior empty-state back to the live header row.
	for col, h := range headers {
		c.table.SetCell(0, col, tview.NewTableCell(h).
			SetTextColor(tcell.ColorSilver).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false).
			SetExpansion(1))
	}

	restoredRow := 0
	for i, s := range snap {
		rowIdx := i + 1 // row 0 is headers
		team := s.Team
		if team == "" {
			team = "-"
		}
		backends := strings.Join(s.Backends, ",")
		if backends == "" {
			backends = "-"
		}
		c.table.SetCell(rowIdx, 0, tview.NewTableCell(s.Namespace).SetTextColor(tcell.ColorWhite))
		c.table.SetCell(rowIdx, 1, tview.NewTableCell(team).SetTextColor(tcell.ColorGray))
		c.table.SetCell(rowIdx, 2, tview.NewTableCell(s.Name).SetTextColor(tcell.ColorWhite))
		c.table.SetCell(rowIdx, 3, tview.NewTableCell(s.Phase).SetTextColor(phaseColor(s.Phase)))
		c.table.SetCell(rowIdx, 4, tview.NewTableCell(fmt.Sprintf("%d", s.Ready)).SetTextColor(tcell.ColorGray))
		c.table.SetCell(rowIdx, 5, tview.NewTableCell(backends).SetTextColor(tcell.ColorGray))
		c.table.SetCell(rowIdx, 6, tview.NewTableCell(agent.FormatAge(s.Created)).SetTextColor(tcell.ColorGray))

		if s.Namespace == selNamespace && s.Name == selName {
			restoredRow = rowIdx
		}
	}

	if restoredRow > 0 {
		c.table.Select(restoredRow, 0)
	} else {
		c.table.Select(1, 0) // first data row
	}
}

// renderEmpty paints the message area when there's nothing (or
// nothing yet) to show. Leaves the header row intact so the column
// labels still orient the user.
func (c *agentListController) renderEmpty(msg string) {
	// Row 0 = headers (already set by caller). Row 1 carries the
	// message spanning the full width.
	cell := tview.NewTableCell(msg).
		SetAlign(tview.AlignLeft).
		SetSelectable(false).
		SetExpansion(1)
	c.table.SetCell(1, 0, cell)
	for col := 1; col < 7; col++ {
		c.table.SetCell(1, col, tview.NewTableCell("").SetSelectable(false))
	}
}

// renderFooter paints the keybinding hint strip. Static content —
// the same keys work regardless of snapshot state.
func (c *agentListController) renderFooter() {
	c.footer.SetText(
		"[#808080]↑/↓ move · a add · r refresh · ↵ drill down (soon) · q/esc quit[-:-:-]",
	)
}

// showDrillDownStub is what Enter does until the per-agent view
// lands. Shows a transient toast naming the selected agent + the
// tracking issue so users who hit Enter don't wonder if their
// keystroke did anything.
func (c *agentListController) showDrillDownStub() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	row, _ := c.table.GetSelection()
	if row <= 0 || row-1 >= len(snap) {
		return
	}
	s := snap[row-1]
	c.footer.SetText(fmt.Sprintf(
		"[#d7af00]drill-down for %s/%s lands in #1450 — for now use `ww agent status %s -n %s`[-:-:-]",
		s.Namespace, s.Name, s.Name, s.Namespace,
	))
	// Restore the key-hint footer after 3s so the toast doesn't stay
	// up forever. AfterFunc is fine — main goroutine reads the text
	// only on repaint, and SetText is thread-safe on tview.TextView.
	time.AfterFunc(3*time.Second, func() {
		c.app.QueueUpdateDraw(c.renderFooter)
	})
}

// ---------------------------------------------------------------------------
// Degraded / no-cluster panel
// ---------------------------------------------------------------------------

// degradedPanel is what Run() shows when kubeconfig resolution failed
// at launch. Same visual language as the live list (frame, title,
// footer) so the user isn't dropped into a different layout — just
// one that says "nothing to show because no cluster."
func degradedPanel(version, contextErr string) tview.Primitive {
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetText(fmt.Sprintf("[::b]ww[-:-:-]  [#a0a0a0]no cluster[-:-:-]  [#808080]· ww %s[-:-:-]", version))

	msg := contextErr
	if msg == "" {
		msg = "No cluster configured — set $KUBECONFIG or pass --kubeconfig."
	}
	body := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("\n\n[#ffaf00]" + msg + "[-:-:-]\n\n" +
			"[#808080]Re-launch with --kubeconfig <path> or set $KUBECONFIG and retry.[-:-:-]")

	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetText("[#808080]q/esc/ctrl-c quit[-:-:-]")

	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(body, 0, 1, false).
		AddItem(footer, 1, 0, false)

	frame := tview.NewFrame(root).SetBorders(0, 0, 0, 0, 1, 1)
	frame.SetBorder(true).
		SetBorderColor(tcell.ColorDimGray).
		SetTitle(" ww tui ").
		SetTitleColor(tcell.ColorSilver)
	return frame
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// phaseColor maps a phase string to the right accent color. Matches
// the kubectl convention: Ready green, Degraded red, Pending amber.
func phaseColor(phase string) tcell.Color {
	switch phase {
	case "Ready":
		return tcell.ColorGreen
	case "Degraded":
		return tcell.ColorOrangeRed
	case "Pending":
		return tcell.ColorGoldenrod
	default:
		return tcell.ColorGray
	}
}

// formatShortDuration renders a duration as "5s", "2m", "1h", etc.
// Matches FormatAge's buckets so the header freshness and the AGE
// column read consistently.
func formatShortDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// ---------------------------------------------------------------------------
// Create-agent modal
// ---------------------------------------------------------------------------

// createAgentState is the mutable buffer the modal form writes into as
// the user types. Read by the Submit callback, cleared by openCreateAgent
// so re-opening the modal starts fresh.
type createAgentState struct {
	name            string
	namespace       string
	backend         string
	team            string
	createNamespace bool
}

// openCreateAgent surfaces the create-agent modal and hands focus to
// its first input. Called from the list's 'a' keybinding. Clears
// prior state so re-opening the modal doesn't show stale values or
// errors from a previous submission.
func (c *agentListController) openCreateAgent() {
	c.createAgentForm.reset(c.defaultCreateNamespace())
	c.pages.ShowPage("create-agent")
	c.app.SetFocus(c.createAgentForm.form)
}

// closeCreateAgent hides the modal and returns focus to the list.
// Called from Submit (on success) and Cancel.
func (c *agentListController) closeCreateAgent() {
	c.pages.HidePage("create-agent")
	c.app.SetFocus(c.table)
}

// defaultCreateNamespace picks the pre-filled namespace value for the
// modal. Uses the current target's namespace when non-empty, falling
// back to ww's default. Matches the namespace-resolution story the
// CLI's logAndResolveNamespace tells.
func (c *agentListController) defaultCreateNamespace() string {
	if c.target != nil && c.target.Namespace != "" {
		return c.target.Namespace
	}
	return agent.DefaultAgentNamespace
}

// createAgentForm bundles the form primitive + mutable state + error
// display. Built once at startup (newCreateAgentModal) and reused
// across each open — reset() wipes the state for the next submission.
type createAgentForm struct {
	form  *tview.Form
	state createAgentState
	err   *tview.TextView
}

// reset wipes the form's state back to sensible defaults. Called
// every time the modal opens so a cancelled or failed previous
// submission doesn't leave stale values or error text behind.
func (f *createAgentForm) reset(ns string) {
	f.state = createAgentState{
		namespace:       ns,
		backend:         agent.DefaultBackend, // echo
		createNamespace: true,
	}
	// Field ordering mirrors the form construction — keep these two
	// in sync if newCreateAgentModal's field order changes.
	f.form.GetFormItem(0).(*tview.InputField).SetText("")       // name
	f.form.GetFormItem(1).(*tview.InputField).SetText(ns)       // namespace
	f.form.GetFormItem(2).(*tview.DropDown).SetCurrentOption(0) // backend
	f.form.GetFormItem(3).(*tview.InputField).SetText("")       // team
	f.form.GetFormItem(4).(*tview.Checkbox).SetChecked(true)    // create-namespace
	f.err.SetText("")
	f.form.SetFocus(0)
}

// newCreateAgentModal builds the tview primitive that represents the
// create-agent modal. Stashes the form on the controller so
// openCreateAgent can wipe state + set focus.
func newCreateAgentModal(ctrl *agentListController) tview.Primitive {
	cf := &createAgentForm{}

	errView := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(true)
	cf.err = errView

	backendTypes := agent.KnownBackends()

	form := tview.NewForm().
		AddInputField("Name", "", 32, nil, func(v string) { cf.state.name = v }).
		AddInputField("Namespace", ctrl.defaultCreateNamespace(), 32, nil, func(v string) { cf.state.namespace = v }).
		AddDropDown("Backend", backendTypes, 0, func(v string, _ int) { cf.state.backend = v }).
		AddInputField("Team (optional)", "", 32, nil, func(v string) { cf.state.team = v }).
		AddCheckbox("Create namespace if missing", true, func(v bool) { cf.state.createNamespace = v }).
		AddButton("Create", func() { submitCreateAgent(ctrl, cf) }).
		AddButton("Cancel", func() { ctrl.closeCreateAgent() })
	form.SetBorder(true).
		SetTitle(" Create agent ").
		SetTitleColor(tcell.ColorSilver).
		SetBorderColor(tcell.ColorDimGray)
	form.SetCancelFunc(func() { ctrl.closeCreateAgent() }) // ESC → cancel
	cf.form = form

	ctrl.createAgentForm = cf

	// Modal composition: center the form + error view vertically
	// (fixed height) and horizontally (fixed width). Surrounding
	// spacer Boxes use flex=1 so the center cell pins while the
	// terminal resizes.
	body := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(errView, 1, 0, false).
		AddItem(form, 0, 1, true)
	return tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(body, 15, 1, true).
			AddItem(tview.NewBox(), 0, 1, false),
			56, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)
}

// submitCreateAgent runs the Create API call in a goroutine so the
// UI doesn't freeze during the Ready wait. The form stays open on
// error (populates the error strip); closes on success and nudges
// the poll loop so the new agent appears in the list within
// milliseconds of the API call returning, not 2s later.
func submitCreateAgent(c *agentListController, cf *createAgentForm) {
	// Validate up front so DNS-1123 errors don't bounce off the
	// apiserver. Same helper the CLI uses.
	state := cf.state
	if err := agent.ValidateName(state.name); err != nil {
		cf.err.SetText("[#ff5f00]" + err.Error() + "[-:-:-]")
		return
	}
	if state.namespace == "" {
		cf.err.SetText("[#ff5f00]namespace is required[-:-:-]")
		return
	}
	if state.team != "" {
		if err := agent.ValidateName(state.team); err != nil {
			cf.err.SetText("[#ff5f00]team: " + err.Error() + "[-:-:-]")
			return
		}
	}

	cf.err.SetText("[#d7af00]creating…[-:-:-]")

	go func() {
		// Wait=false: we let the poll loop show the Pending → Ready
		// transition instead of freezing the modal for up to 2
		// minutes. The moment the CR is accepted, we close the
		// modal and force a refresh tick so the row appears.
		err := agent.Create(
			context.Background(),
			c.target,
			c.cfg,
			nil,
			agent.CreateOptions{
				Name:      state.name,
				Namespace: state.namespace,
				Backends: []agent.BackendSpec{{
					Name: state.backend,
					Type: state.backend,
					Port: agent.BackendPort(0),
				}},
				CLIVersion:      c.version,
				CreatedBy:       "ww tui · agent add",
				Team:            state.team,
				CreateNamespace: state.createNamespace,
				AssumeYes:       true,   // skip k8s.Confirm (TUI can't prompt over tview)
				Wait:            false,  // poll loop shows the phase flip
				Out:             discardWriter{}, // swallow Create's stdout chatter
				In:              nil,
			},
		)
		c.app.QueueUpdateDraw(func() {
			if err != nil {
				cf.err.SetText("[#ff5f00]" + err.Error() + "[-:-:-]")
				return
			}
			c.closeCreateAgent()
			// Nudge the poll goroutine so the new row appears now,
			// not on the next scheduled tick.
			select {
			case c.refreshNow <- struct{}{}:
			default:
			}
		})
	}()
}

// discardWriter is an io.Writer that throws everything away. Used
// as opts.Out when invoking agent.Create from the TUI — we don't
// want the CLI-style banner chatter leaking into the terminal
// underneath the tview canvas. io.Discard would work too but
// importing `io` here just for that is overkill.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
