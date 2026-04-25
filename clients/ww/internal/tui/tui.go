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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
	"github.com/skthomasjr/witwave/clients/ww/internal/config"
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

	// Global quit bindings. Ctrl-C and 'q' always exit the app from
	// every page. ESC is DELIBERATELY not handled here — pages use
	// it as a "go back / close modal" affordance (logs view → list,
	// create-agent modal → list) and catching it at the app level
	// would swallow the keystroke before the page's InputCapture
	// runs. Users who need an emergency bail have Ctrl-C.
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
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
			// Reserved for the per-agent details view (status,
			// events, conversation log) — when it lands, Enter
			// will be the obvious "drill in" key. For now it
			// flashes a short hint in the footer so the keystroke
			// does the obvious thing rather than silently no-op.
			ctrl.showDetailsStub()
			return nil
		case tcell.KeyEscape:
			// ESC at the list level = quit. Inner pages (logs,
			// create-agent modal) use ESC as "go back," but the
			// list is the root — there's nowhere to go back to,
			// so preserve kubectl/k9s muscle memory here.
			ctrl.app.Stop()
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
			case 'd':
				ctrl.openDeleteAgent()
				return nil
			case 'l':
				ctrl.openAgentLogs()
				return nil
			case 's':
				ctrl.openAgentSend()
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
		"[#808080]↑/↓ move · a add · d delete · s send · l logs · r refresh · ↵ details (soon) · q/esc quit[-:-:-]",
	)
}

// showDetailsStub is what Enter does until the per-agent details
// view lands (status / events / conversation log / send-prompt
// tabs). Flashes a 3-second hint in the footer pointing users at
// the keys that DO work today, then restores the canonical
// keybinding strip. AfterFunc is fine here — SetText on
// tview.TextView is goroutine-safe and we route through
// QueueUpdateDraw on restoration so the repaint stays on the UI
// thread.
func (c *agentListController) showDetailsStub() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	row, _ := c.table.GetSelection()
	if row <= 0 || row-1 >= len(snap) {
		return
	}
	s := snap[row-1]
	c.footer.SetText(fmt.Sprintf(
		"[#d7af00]details view for %s/%s coming soon — try `l` for logs or `ww agent status %s -n %s` from the CLI[-:-:-]",
		s.Namespace, s.Name, s.Name, s.Namespace,
	))
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
//
// Mirrors `ww agent create`'s flag surface — when the form grows new
// fields, populate them here rather than threading args through the
// submit closure. CreateOptions construction is the single sink.
type createAgentState struct {
	name            string
	namespace       string
	backend         string
	team            string
	createNamespace bool

	// Backend credentials (LLM backends only). authMode names which
	// of the three CLI flags this maps to: "" / "none" → no Secret;
	// "profile" → --auth claude=oauth; "from-env" → --auth-from-env;
	// "existing" → --auth-secret. authValue is the per-mode payload.
	authMode  string
	authValue string

	// GitOps repo. Empty = pure cluster-side create. Non-empty =
	// after the CR lands, run scaffold (idempotent, merges with any
	// existing layout) then attach gitSync via the same auth-from-gh
	// path the CLI uses by default.
	gitopsRepo string
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

// reset wipes the form's state back to defaults — the layered
// resolution (env > saved > fallback) handled by loadTUIDefaults.
// Called every time the modal opens so a cancelled or failed
// previous submission doesn't leave stale values or error text
// behind, but successful prior submissions DO carry through via
// the saved file.
func (f *createAgentForm) reset(ctxNamespace string) {
	d := loadTUIDefaults()

	// Namespace: env-pinned > saved > the controller's notion of the
	// "current" namespace (kubeconfig context or witwave fallback).
	// loadTUIDefaults already returns the layered value, but if no
	// env / saved layer fired we want the kubeconfig-context's ns
	// rather than the hard-coded fallback. The saved-file probe via
	// config.LoadTUICreateDefaults answers "did anything actually
	// save a value?" — when no, defer to whatever the kubeconfig
	// context is pointing at.
	if d.Namespace == agent.DefaultAgentNamespace && os.Getenv("WW_TUI_DEFAULT_NAMESPACE") == "" {
		if _, savedOK := config.LoadTUICreateDefaults(os.Getenv); !savedOK {
			d.Namespace = ctxNamespace
		}
	}

	f.state = createAgentState{
		namespace:       d.Namespace,
		backend:         d.Backend,
		team:            d.Team,
		createNamespace: d.CreateNamespace,
		authMode:        d.AuthMode,
		authValue:       d.AuthValue,
		gitopsRepo:      d.GitOpsRepo,
	}

	// Push the resolved values into the form widgets. Field indices
	// match the AddInputField / AddDropDown / AddCheckbox order in
	// newCreateAgentModal; keep them in sync on field reorders.
	f.form.GetFormItem(0).(*tview.InputField).SetText("")          // name (always blank)
	f.form.GetFormItem(1).(*tview.InputField).SetText(d.Namespace) // namespace
	f.form.GetFormItem(2).(*tview.DropDown).SetCurrentOption(dropdownIndexOrZero(agent.KnownBackends(), d.Backend))
	f.form.GetFormItem(3).(*tview.InputField).SetText(d.Team) // team
	f.form.GetFormItem(4).(*tview.Checkbox).SetChecked(d.CreateNamespace)
	f.form.GetFormItem(5).(*tview.DropDown).SetCurrentOption(dropdownIndexOrZero([]string{"none", "profile", "from-env", "existing-secret", "set-inline"}, d.AuthMode))
	f.form.GetFormItem(6).(*tview.InputField).SetText(d.AuthValue)  // auth value
	f.form.GetFormItem(7).(*tview.InputField).SetText(d.GitOpsRepo) // gitops repo
	f.err.SetText("")
	f.form.SetFocus(0)
}

// dropdownIndexOrZero finds the position of `value` in `options`,
// returning 0 (the first option) when not found. Defensive against
// a saved value drifting out of the catalog (e.g. a backend type
// got renamed between releases) — better to land on a valid first
// option than to leave the dropdown empty / desync'd.
func dropdownIndexOrZero(options []string, value string) int {
	for i, o := range options {
		if o == value {
			return i
		}
	}
	return 0
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

	authModes := []string{"none", "profile", "from-env", "existing-secret", "set-inline"}

	// Field width 36 + 36-char label column = ~72 cols of content,
	// fits inside the modal's 90 + 2 border. Placeholders show
	// example values inline so users don't have to guess at the
	// expected shape (especially for repo URLs).
	form := tview.NewForm().
		AddInputField("Name", "", 36, nil, func(v string) { cf.state.name = v }).
		AddInputField("Namespace", ctrl.defaultCreateNamespace(), 36, nil, func(v string) { cf.state.namespace = v }).
		AddDropDown("Backend", backendTypes, 0, func(v string, _ int) { cf.state.backend = v }).
		AddInputField("Team (optional)", "", 36, nil, func(v string) { cf.state.team = v }).
		AddCheckbox("Create namespace (if missing)", true, func(v bool) { cf.state.createNamespace = v }).
		AddDropDown("Auth mode (LLM backends)", authModes, 0, func(v string, _ int) { cf.state.authMode = v }).
		AddInputField("Auth value (profile / VAR / Secret)", "", 36, nil, func(v string) { cf.state.authValue = v }).
		AddInputField("GitOps repo (optional)", "", 36, nil, func(v string) { cf.state.gitopsRepo = v }).
		AddButton("Create", func() { submitCreateAgent(ctrl, cf) }).
		AddButton("Cancel", func() { ctrl.closeCreateAgent() })

	// Placeholders — grayed-out example text that disappears when
	// the user starts typing. Picks the most common case for each
	// field so newcomers see the expected shape at a glance.
	setInputPlaceholder(form, 0, "iris")
	setInputPlaceholder(form, 3, "research")
	setInputPlaceholder(form, 6, "oauth · ANTHROPIC_API_KEY · my-secret · KEY1=v1,KEY2=v2")
	setInputPlaceholder(form, 7, "owner/repo  (e.g. skthomasjr/witwave-test)")

	form.SetBorder(true).
		SetTitle(" Create agent ").
		SetTitleColor(tcell.ColorSilver).
		SetBorderColor(tcell.ColorDimGray)
	form.SetButtonsAlign(tview.AlignCenter)
	// One-row item padding so the long-form layout has visible
	// breathing room between fields. The taller modal accommodates.
	form.SetItemPadding(1)
	form.SetCancelFunc(func() { ctrl.closeCreateAgent() }) // ESC → cancel

	// Arrow-key navigation. tview.Form natively handles Tab /
	// Shift-Tab; users (myself included) instinctively press Up /
	// Down. Translate them at the form's input-capture layer by
	// returning a synthesised Tab / Backtab event — tview handles
	// that as a regular focus-cycle.
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDown:
			return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		case tcell.KeyUp:
			return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
		}
		return event
	})

	cf.form = form

	ctrl.createAgentForm = cf

	// Always-visible hint strip below the form so the user sees how
	// to submit before they have to tab-hunt for the buttons.
	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[#808080]Tab / ↑↓ navigate · Enter submit · ESC cancel[-:-:-]")

	// Modal composition: error strip | form (fills) | hint strip.
	// Long-form layout: width 90 fits the 35-char labels + 36-wide
	// inputs comfortably; height 30 accommodates 8 fields with
	// 1-row item padding (8 fields + 7 gaps = 15) + 2-line border
	// + 1-row error + 1-row hint + 2-row button area = 21, plus a
	// few rows of breathing room for placeholder rendering.
	body := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(errView, 1, 0, false).
		AddItem(form, 0, 1, true).
		AddItem(hint, 1, 0, false)
	return tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(body, 30, 1, true).
			AddItem(tview.NewBox(), 0, 1, false),
			90, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)
}

// setInputPlaceholder is a tiny helper that pulls a form item by
// index and applies a placeholder string. Centralised so the create
// modal's "show example data" config reads as a single block of
// SetInputField → setInputPlaceholder calls. No-op if the item at
// that index isn't an InputField (defensive against future field
// reorder).
func setInputPlaceholder(form *tview.Form, idx int, text string) {
	if input, ok := form.GetFormItem(idx).(*tview.InputField); ok {
		input.SetPlaceholder(text)
		input.SetPlaceholderTextColor(tcell.ColorGray)
	}
}

// submitCreateAgent runs the Create API call (and, when --repo is
// set, scaffold + git add) in a goroutine so the UI doesn't freeze.
// The form stays open on error with the error strip naming the step
// that failed (create vs. scaffold vs. git-add), so the user can
// adjust + retry without re-typing the rest of the form.
//
// Three sequential phases:
//
//  1. Create the CR (with credentials wired from the auth fields).
//  2. If gitopsRepo is set: scaffold the repo (idempotent).
//  3. If gitopsRepo is set: attach gitSync via --auth-from-gh.
//
// Each phase has its own banner state so the user sees progress;
// failures short-circuit (don't run later phases against
// half-applied state). Wait=false on Create so the modal closes as
// soon as the CR is accepted — the list's poll loop renders the
// Pending → Ready transition naturally.
func submitCreateAgent(c *agentListController, cf *createAgentForm) {
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
	// Resolve auth flags from the dropdown + value field. "none"
	// short-circuits to BackendAuthNone (legitimate for echo).
	auth, authErr := resolveTUIAuth(state.backend, state.authMode, state.authValue)
	if authErr != nil {
		cf.err.SetText("[#ff5f00]auth: " + authErr.Error() + "[-:-:-]")
		return
	}

	cf.err.SetText("[#d7af00]creating CR…[-:-:-]")

	go func() {
		// Phase 1 — Create the CR.
		var backendAuth []agent.BackendAuthResolver
		if auth.Mode != agent.BackendAuthNone {
			backendAuth = []agent.BackendAuthResolver{auth}
		}
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
				BackendAuth:     backendAuth,
				AssumeYes:       true,
				Wait:            false,
				Out:             discardWriter{},
				In:              nil,
			},
		)
		if err != nil {
			c.app.QueueUpdateDraw(func() {
				cf.err.SetText("[#ff5f00]create: " + err.Error() + "[-:-:-]")
			})
			return
		}

		// Phase 1 succeeded — persist the values just used as the
		// new last-used defaults. Skips the agent name (always
		// different per create); everything else is sticky muscle
		// memory for the next launch. Best-effort write — failures
		// are silent because the CR has already landed.
		saveTUIDefaults(tuiDefaults{
			Namespace:       state.namespace,
			Backend:         state.backend,
			Team:            state.team,
			CreateNamespace: state.createNamespace,
			AuthMode:        state.authMode,
			AuthValue:       state.authValue,
			GitOpsRepo:      state.gitopsRepo,
		})

		// Phase 2 + 3 are GitOps-side, only when the user set --repo.
		// Empty repo = pure cluster-side create; we're done.
		if state.gitopsRepo == "" {
			c.app.QueueUpdateDraw(func() {
				c.closeCreateAgent()
				select {
				case c.refreshNow <- struct{}{}:
				default:
				}
			})
			return
		}

		// Phase 2 — scaffold the repo (idempotent).
		c.app.QueueUpdateDraw(func() {
			cf.err.SetText("[#d7af00]scaffolding repo…[-:-:-]")
		})
		if err := agent.Scaffold(context.Background(), agent.ScaffoldOptions{
			Name: state.name,
			Repo: state.gitopsRepo,
			Backends: []agent.BackendSpec{{
				Name: state.backend,
				Type: state.backend,
				Port: agent.BackendPort(0),
			}},
			CLIVersion: c.version,
			Out:        discardWriter{},
		}); err != nil {
			c.app.QueueUpdateDraw(func() {
				cf.err.SetText("[#ff5f00]scaffold: " + err.Error() +
					" [#a0a0a0](CR created; retry the gitOps phase via `ww agent git add` or `ww agent scaffold` from the CLI)[-:-:-]")
			})
			return
		}

		// Phase 3 — attach gitSync (default to gh auth token).
		c.app.QueueUpdateDraw(func() {
			cf.err.SetText("[#d7af00]attaching gitSync…[-:-:-]")
		})
		if err := agent.GitAdd(context.Background(), c.cfg, agent.GitAddOptions{
			Agent:     state.name,
			Namespace: state.namespace,
			Repo:      state.gitopsRepo,
			Auth:      agent.GitAuthResolver{Mode: agent.GitAuthFromGH},
			AssumeYes: true,
			Out:       discardWriter{},
		}); err != nil {
			c.app.QueueUpdateDraw(func() {
				cf.err.SetText("[#ff5f00]git add: " + err.Error() +
					" [#a0a0a0](CR + repo scaffold succeeded; retry `ww agent git add` from the CLI)[-:-:-]")
			})
			return
		}

		c.app.QueueUpdateDraw(func() {
			c.closeCreateAgent()
			select {
			case c.refreshNow <- struct{}{}:
			default:
			}
		})
	}()
}

// resolveTUIAuth maps the modal's authMode dropdown + authValue input
// into a BackendAuthResolver the CLI's create path understands. The
// dropdown's display strings (none / profile / from-env / existing-
// secret) name the variant; the value field's contents fill in the
// per-mode payload (profile name / env var(s) / Secret name).
func resolveTUIAuth(backendName, mode, value string) (agent.BackendAuthResolver, error) {
	switch mode {
	case "", "none":
		return agent.BackendAuthResolver{Mode: agent.BackendAuthNone}, nil
	case "profile":
		if value == "" {
			return agent.BackendAuthResolver{}, fmt.Errorf("profile mode needs an Auth value (e.g. oauth, api-key)")
		}
		return agent.BackendAuthResolver{
			Backend: backendName,
			Mode:    agent.BackendAuthProfile,
			Profile: value,
		}, nil
	case "from-env":
		if value == "" {
			return agent.BackendAuthResolver{}, fmt.Errorf("from-env mode needs an Auth value (e.g. CLAUDE_CODE_OAUTH_TOKEN)")
		}
		// Same comma-split convention as the CLI's --auth-from-env.
		vars := strings.Split(value, ",")
		out := make([]string, 0, len(vars))
		for _, v := range vars {
			v = strings.TrimSpace(v)
			if v != "" {
				out = append(out, v)
			}
		}
		return agent.BackendAuthResolver{
			Backend: backendName,
			Mode:    agent.BackendAuthFromEnv,
			EnvVars: out,
		}, nil
	case "existing-secret":
		if value == "" {
			return agent.BackendAuthResolver{}, fmt.Errorf("existing-secret mode needs an Auth value (the K8s Secret name)")
		}
		return agent.BackendAuthResolver{
			Backend:        backendName,
			Mode:           agent.BackendAuthExistingSecret,
			ExistingSecret: value,
		}, nil
	case "set-inline":
		if value == "" {
			return agent.BackendAuthResolver{}, fmt.Errorf("set-inline mode needs an Auth value (e.g. KEY1=val1,KEY2=val2)")
		}
		// Comma-separated KEY=VALUE pairs — same shape the CLI's
		// --auth-set accepts on `backend add` (without the
		// <backend>: prefix; backend's already named on the form).
		// Whitespace tolerated around each pair so users can paste
		// "KEY1=v1, KEY2=v2" without having to scrub spaces.
		pairs := strings.Split(value, ",")
		inline := make(map[string]string, len(pairs))
		for _, raw := range pairs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			k, v, err := agent.SplitInlineKV(raw, "set-inline")
			if err != nil {
				return agent.BackendAuthResolver{}, err
			}
			if existing, dup := inline[k]; dup {
				return agent.BackendAuthResolver{}, fmt.Errorf(
					"set-inline: key %q given twice (first=%q, second=%q) — pick one",
					k, existing, v,
				)
			}
			inline[k] = v
		}
		if len(inline) == 0 {
			return agent.BackendAuthResolver{}, fmt.Errorf("set-inline mode needs at least one KEY=VALUE pair")
		}
		return agent.BackendAuthResolver{
			Backend: backendName,
			Mode:    agent.BackendAuthInline,
			Inline:  inline,
		}, nil
	}
	return agent.BackendAuthResolver{}, fmt.Errorf("unknown auth mode %q", mode)
}

// discardWriter is an io.Writer that throws everything away. Used
// as opts.Out when invoking agent.Create from the TUI — we don't
// want the CLI-style banner chatter leaking into the terminal
// underneath the tview canvas. io.Discard would work too but
// importing `io` here just for that is overkill.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
