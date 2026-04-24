package tui

import (
	"context"
	"fmt"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// deletePageName is the Pages id for the delete-confirm modal.
// Single page; rebuilt per-open so the agent identity it operates on
// can't drift between when 'd' was pressed and when Delete clicks.
const deletePageName = "delete-agent"

// deleteAgentState mirrors the CR-side flag set on `ww agent delete`:
// each toggle maps directly to a CreateOptions/DeleteOptions field.
// `purge` is the convenience superset (all destructive flags on);
// flipping it ticks the per-flag boxes too so the UI reflects what
// will actually happen.
type deleteAgentState struct {
	removeRepoFolder bool
	deleteGitSecret  bool
	purge            bool
}

// deleteAgentForm bundles the form primitive + state + error view.
// Same structural pattern as createAgentForm — the controller owns
// it and rebuilds it each open so a previous submission's state
// can't leak into the next confirmation.
type deleteAgentForm struct {
	form  *tview.Form
	state deleteAgentState
	err   *tview.TextView

	// agent + namespace are captured at open-time so the controller
	// knows what the form will operate on even if the user moves the
	// list selection in another window. (tview is single-app so this
	// is theoretical, but the contract reads cleanly.)
	agent     string
	namespace string
}

// openDeleteAgent triggers the modal from the list's 'd' keybinding.
// Captures the currently-selected agent + namespace, builds a fresh
// form, swaps it onto the Pages container, and grabs focus.
func (c *agentListController) openDeleteAgent() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	row, _ := c.table.GetSelection()
	if row <= 0 || row-1 >= len(snap) {
		return
	}
	s := snap[row-1]

	form := buildDeleteAgentForm(c, s.Name, s.Namespace)

	if c.pages.HasPage(deletePageName) {
		c.pages.RemovePage(deletePageName)
	}
	c.pages.AddPage(deletePageName, form.modalRoot, true, true)
	c.app.SetFocus(form.form)
}

// closeDeleteAgent removes the modal and returns focus to the list.
// Called on Cancel + on successful delete + on ESC.
func (c *agentListController) closeDeleteAgent() {
	c.pages.RemovePage(deletePageName)
	c.app.SetFocus(c.table)
}

// deleteAgentFormBundle adds the modal-root primitive that the
// controller's HidePage / AddPage calls operate on, alongside the
// form fields the submit handler reads.
type deleteAgentFormBundle struct {
	form      *tview.Form
	modalRoot tview.Primitive
	state     *deleteAgentState
	err       *tview.TextView
	agent     string
	namespace string
}

// buildDeleteAgentForm constructs the modal for the given target.
// Built fresh per-open (vs. once-and-reset) because the agent +
// namespace are baked into the form's title and submit closure —
// reusing across different agents would risk submitting against the
// wrong target if the controller re-used state by mistake.
func buildDeleteAgentForm(ctrl *agentListController, agentName, namespace string) *deleteAgentFormBundle {
	state := &deleteAgentState{}

	errView := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(true)

	bundle := &deleteAgentFormBundle{
		state:     state,
		err:       errView,
		agent:     agentName,
		namespace: namespace,
	}

	form := tview.NewForm().
		AddCheckbox("Remove repo folder (.agents/<…>/)", false, func(v bool) {
			state.removeRepoFolder = v
		}).
		AddCheckbox("Delete ww-managed credential Secret(s)", false, func(v bool) {
			state.deleteGitSecret = v
		}).
		AddCheckbox("Purge (everything ww-managed about this agent)", false, func(v bool) {
			state.purge = v
			// `purge` is a convenience: ticking it ticks the two
			// per-flag boxes too so the form reflects the actual
			// blast radius. Untick is one-way (doesn't auto-untick
			// the granular flags) — same semantics as the CLI's
			// --purge mapping to --remove-repo-folder + --delete-
			// git-secret.
			if v {
				state.removeRepoFolder = true
				state.deleteGitSecret = true
				// GetFormItem indexes match the AddCheckbox order
				// above; keep them in sync if you reorder.
				bundle.form.GetFormItem(0).(*tview.Checkbox).SetChecked(true)
				bundle.form.GetFormItem(1).(*tview.Checkbox).SetChecked(true)
			}
		}).
		AddButton("Delete", func() { submitDeleteAgent(ctrl, bundle) }).
		AddButton("Cancel", func() { ctrl.closeDeleteAgent() })

	form.SetBorder(true).
		SetTitle(fmt.Sprintf(" Delete %s/%s ", namespace, agentName)).
		SetTitleColor(tcell.ColorOrangeRed). // destructive — make the chrome speak it
		SetBorderColor(tcell.ColorOrangeRed)
	form.SetButtonsAlign(tview.AlignCenter)
	form.SetItemPadding(0)
	form.SetCancelFunc(func() { ctrl.closeDeleteAgent() })

	// Arrow-key navigation — same translation the create modal uses
	// (Up → Backtab, Down → Tab) so muscle memory carries over
	// between the two destructive forms.
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDown:
			return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		case tcell.KeyUp:
			return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
		}
		return event
	})

	bundle.form = form

	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[#808080]Tab / ↑↓ navigate · Enter submit · ESC cancel[-:-:-]")

	// Confirmation context line above the checkboxes — destructive
	// op, the user gets one extra read of "this is what's about to
	// happen" before clicking Delete.
	confirm := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(true).
		SetText(fmt.Sprintf(
			"[#ffaf00]About to delete WitwaveAgent %s/%s.[-:-:-]\n"+
				"[#a0a0a0]Operator cascades pod + Service teardown via owner refs.\n"+
				"Tick options below for additional cleanup. Untouched defaults\n"+
				"leave the gitSync repo + credential Secrets in place.[-:-:-]",
			namespace, agentName,
		))

	body := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(confirm, 4, 0, false).
		AddItem(errView, 1, 0, false).
		AddItem(form, 0, 1, true).
		AddItem(hint, 1, 0, false)

	bundle.modalRoot = tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(body, 16, 1, true).
			AddItem(tview.NewBox(), 0, 1, false),
			68, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)

	return bundle
}

// submitDeleteAgent runs the delete asynchronously so the modal
// doesn't freeze. Same async pattern the create modal uses; closes
// on success, error stays inline so the user can adjust + retry
// without re-typing options. The list's poll loop picks up the
// delete on its next tick — and we ping refreshNow so the row
// disappears within milliseconds rather than 2s later.
func submitDeleteAgent(c *agentListController, b *deleteAgentFormBundle) {
	state := *b.state
	b.err.SetText("[#d7af00]deleting…[-:-:-]")

	go func() {
		err := agent.Delete(
			context.Background(),
			c.target,
			c.cfg,
			agent.DeleteOptions{
				Name:             b.agent,
				Namespace:        b.namespace,
				RemoveRepoFolder: state.removeRepoFolder,
				DeleteGitSecret:  state.deleteGitSecret,
				AssumeYes:        true,            // skip k8s.Confirm — TUI can't prompt over tview
				Out:              discardWriter{}, // swallow Delete's banner chatter
			},
		)
		c.app.QueueUpdateDraw(func() {
			if err != nil {
				b.err.SetText("[#ff5f00]" + err.Error() + "[-:-:-]")
				return
			}
			c.closeDeleteAgent()
			select {
			case c.refreshNow <- struct{}{}:
			default:
			}
		})
	}()
}
