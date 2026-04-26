package tui

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// sendPageName is the Pages id for the send-message modal. Single
// page; rebuilt fresh per-open so the agent identity it operates on
// can't drift between when 's' was pressed and when Send fires.
const sendPageName = "agent-send"

// sendDefaultTimeout matches the CLI's default round-trip cap (30s).
// LLM backends with cold-start delays often take 5-15s; 30s leaves
// buffer for the long tail without trapping users in a hung modal.
const sendDefaultTimeout = 30 * time.Second

// sendAgentForm bundles the form primitive + state + response view.
// Built fresh per `s` keystroke so the previous interaction's
// response doesn't bleed into the new one. The in-flight Send
// runs in a goroutine; the controller flips a "sending" flag under
// mu so a second Send press during a hung first call doesn't
// double-fire.
type sendAgentForm struct {
	form     *tview.Form
	response *tview.TextView
	modal    tview.Primitive

	agent     string
	namespace string

	// targets is the dropdown's value list. First entry is "(agent
	// — harness routes)" which sends with no backend_id (default
	// behaviour); subsequent entries are each declared backend's
	// name, which stamp metadata.backend_id to bypass routing.
	targets []string

	mu      sync.Mutex
	sending bool
	// active tracks whether the modal is still mounted. In-flight
	// Send goroutines check this under mu before drawing so a
	// response that lands after the user has ESC'd / Cancelled /
	// navigated away doesn't paint into a detached (or recycled)
	// form. Set true when the page is added; cleared on every
	// close path.
	active bool
	target string // currently-selected dropdown value
	prompt string
}

// openAgentSend pops the send-message modal scoped to whichever row
// was selected on the list. Reuses the same Pages-overlay pattern
// the create + delete modals use so the navigation chrome stays
// consistent.
func (c *agentListController) openAgentSend() {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	row, _ := c.table.GetSelection()
	if row <= 0 || row-1 >= len(snap) {
		return
	}
	s := snap[row-1]

	form := buildSendAgentForm(c, s.Name, s.Namespace, s.Backends)

	// If a previous send modal is still tracked (rare — close paths
	// normally clear it), mark it inactive so any in-flight goroutine
	// from that prior open doesn't paint into the freshly-built form.
	if c.sendForm != nil {
		c.sendForm.mu.Lock()
		c.sendForm.active = false
		c.sendForm.mu.Unlock()
	}

	if c.pages.HasPage(sendPageName) {
		c.pages.RemovePage(sendPageName)
	}
	form.mu.Lock()
	form.active = true
	form.mu.Unlock()
	c.sendForm = form
	c.pages.AddPage(sendPageName, form.modal, true, true)
	c.app.SetFocus(form.form)
}

// closeAgentSend hides the modal and returns focus to the list.
// Called from Cancel + ESC + on successful round-trip via the
// "[Close]" button that replaces "[Send]" once a response lands.
func (c *agentListController) closeAgentSend() {
	// Mark the form inactive before tearing it down so any pending
	// Send goroutine (still mid-flight against the backend) skips
	// its QueueUpdateDraw paint instead of writing into a detached
	// — or already-recycled — response view.
	if c.sendForm != nil {
		c.sendForm.mu.Lock()
		c.sendForm.active = false
		c.sendForm.mu.Unlock()
		c.sendForm = nil
	}
	c.pages.RemovePage(sendPageName)
	c.app.SetFocus(c.table)
}

// buildSendAgentForm constructs the modal for a specific (agent,
// namespace) pair. Backend list comes from the snapshot so we
// don't fetch the CR a second time at modal-open. Layout:
//
//	╭─ ww tui · send · <ns>/<agent> ──╮
//	│ Target backend  [agent ▾]       │  ← form
//	│ Prompt          [...........]   │
//	│ [Send]   [Cancel]               │
//	│                                 │
//	│ Response:                       │  ← scrollable response view
//	│ ┌────────────────────────────┐  │
//	│ │ (waiting…)                 │  │
//	│ └────────────────────────────┘  │
//	│ Tab navigate · ESC back         │  ← hint
//	╰─────────────────────────────────╯
func buildSendAgentForm(ctrl *agentListController, agentName, namespace string, backends []string) *sendAgentForm {
	f := &sendAgentForm{
		agent:     agentName,
		namespace: namespace,
	}
	// Dropdown values: "(agent — harness routes)" first, then each
	// declared backend by name. The harness-routed entry maps to
	// SendOptions.BackendID="" — explicit no-op metadata; the
	// harness picks per backend.yaml.
	f.targets = []string{"(agent — harness routes)"}
	f.targets = append(f.targets, backends...)
	f.target = f.targets[0]

	// Response area — scrollable TextView, dynamic colors off (raw
	// JSON or plain text from the backend, [color tags] would mis-
	// parse). Wrap on so long lines flow rather than truncate.
	f.response = tview.NewTextView().
		SetDynamicColors(false).
		SetScrollable(true).
		SetWrap(true).
		SetText("(waiting for prompt…)")
	f.response.SetBorder(true).
		SetBorderColor(tcell.ColorDimGray).
		SetTitle(" response ").
		SetTitleColor(tcell.ColorSilver)

	form := tview.NewForm().
		AddDropDown("Target", f.targets, 0, func(v string, _ int) { f.target = v }).
		AddInputField("Prompt", "", 60, nil, func(v string) { f.prompt = v }).
		AddButton("Send", func() { submitSendAgent(ctrl, f) }).
		AddButton("Cancel", func() { ctrl.closeAgentSend() })

	form.SetBorder(true).
		SetTitle(fmt.Sprintf(" Send to %s/%s ", namespace, agentName)).
		SetTitleColor(tcell.ColorSilver).
		SetBorderColor(tcell.ColorDimGray)
	form.SetButtonsAlign(tview.AlignCenter)
	form.SetItemPadding(1)
	form.SetCancelFunc(func() { ctrl.closeAgentSend() })

	// Same arrow-key translation the create + delete modals use —
	// muscle memory carries across.
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyDown:
			return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		case tcell.KeyUp:
			return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
		}
		return event
	})

	// Placeholder hint for the prompt field — users hitting `s` for
	// the first time see something concrete to type.
	setInputPlaceholder(form, 1, "ping · what is your name? · summarise the latest events")

	f.form = form

	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[#808080]Tab / ↑↓ navigate · Enter send · ESC back[-:-:-]")

	// Vertical Flex: form (fixed-ish height for fields + buttons),
	// response (flex-fill, scrollable), hint strip.
	body := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(form, 9, 0, true).
		AddItem(f.response, 0, 1, false).
		AddItem(hint, 1, 0, false)

	// Center the body horizontally with flex-1 spacers; vertical
	// width 90 keeps long prompts readable without word-wrapping
	// on every other character.
	f.modal = tview.NewFlex().
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(body, 24, 1, true).
			AddItem(tview.NewBox(), 0, 1, false),
			90, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)

	return f
}

// submitSendAgent kicks off the round-trip in a goroutine. Disables
// re-entry via the sending flag so a stuck call can't pile up extra
// goroutines from impatient repeat-Enter presses. Response text
// flows into f.response; errors land there too with a red marker
// so the modal stays open for retry without re-typing.
func submitSendAgent(c *agentListController, f *sendAgentForm) {
	f.mu.Lock()
	if f.sending {
		f.mu.Unlock()
		return // already in flight; ignore the second click
	}
	if f.prompt == "" {
		f.response.SetText("(prompt is empty — type something and press Send)")
		f.mu.Unlock()
		return
	}
	f.sending = true
	target := f.target
	prompt := f.prompt
	f.mu.Unlock()

	// Resolve the target dropdown value to a SendOptions.BackendID.
	// First entry ("(agent — harness routes)") translates to "" so
	// the harness picks per backend.yaml; everything else is a
	// concrete backend name that becomes metadata.backend_id.
	var backendID string
	if target != f.targets[0] {
		backendID = target
	}

	f.response.SetText(fmt.Sprintf("(sending to %s …)", target))

	go func() {
		var buf bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), sendDefaultTimeout)
		defer cancel()
		err := agent.Send(ctx, c.cfg, agent.SendOptions{
			Agent:     f.agent,
			Namespace: f.namespace,
			Prompt:    prompt,
			BackendID: backendID,
			Timeout:   sendDefaultTimeout,
			Out:       &buf,
		})

		f.mu.Lock()
		f.sending = false
		f.mu.Unlock()

		c.app.QueueUpdateDraw(func() {
			// Bail if the modal was closed (ESC / Cancel / page
			// navigation) while the request was in flight. Painting
			// into a detached response view causes the next opened
			// send modal to flash the prior call's text, even though
			// the form was rebuilt fresh. Checked under mu to pair
			// with the close-path writers.
			f.mu.Lock()
			active := f.active
			f.mu.Unlock()
			if !active {
				return
			}
			if err != nil {
				f.response.SetText(fmt.Sprintf("ERROR: %s\n\n(prompt left in form — adjust + retry, or ESC to back out)", err.Error()))
				return
			}
			text := buf.String()
			if text == "" {
				text = "(empty response)"
			}
			f.response.SetText(text)
			// Scroll to the top so the start of the response is
			// visible — long replies otherwise open at the bottom
			// and force the user to scroll up to see the lede.
			f.response.ScrollToBeginning()
		})
	}()
}
