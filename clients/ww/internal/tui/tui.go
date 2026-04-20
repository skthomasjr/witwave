// Package tui implements the `ww tui` interactive surface (#1450).
//
// Current state: stub. One screen with a welcome banner + context
// confirmation + tracking-issue pointer. No cluster API calls; no
// feature panels yet. Establishes the bubbletea framework that
// future panels (status, logs, events, session drill-down) will
// plug into.
//
// Design notes:
//
//   - Single persistent view. No splash-then-main transition —
//     bubbletea apps launch fast enough that a timed splash would
//     feel like an artificial pause. The welcome sits directly in
//     the main view's header area.
//   - Graceful degradation. If kubeconfig resolution fails
//     (missing file, no current-context, parse error) the TUI
//     still launches and shows a "No cluster configured"
//     message. Never blocks on missing config.
//   - Exit in three forms (q / esc / ctrl-c) all routed to the
//     same clean-shutdown path. No confirm dialog for a read-only
//     surface.
package tui

import (
	"fmt"
	"strings"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the bubbletea Model for the TUI stub. Exported so tests
// (when we add them alongside real panels) can construct one
// directly without going through the entry-point cobra command.
type Model struct {
	version    string
	target     *k8s.Target
	contextErr string // non-empty when kubeconfig resolution failed; shown in place of the cluster block

	width  int
	height int
}

// New builds an initial Model with the resolved context (or the
// diagnostic message from a failed resolution). Always succeeds —
// a TUI that can't launch because of kubeconfig issues would be
// the wrong UX for an interactive surface.
func New(version string, target *k8s.Target, contextErr string) Model {
	return Model{
		version:    version,
		target:     target,
		contextErr: contextErr,
	}
}

// Run starts the bubbletea program and blocks until the user
// quits. The alt-screen is entered automatically; the terminal is
// restored to its pre-launch state on exit via bubbletea's own
// shutdown path.
func Run(m Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Init is the bubbletea Model contract. No startup commands for the
// stub — future panels will kick off data loads here.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update routes keyboard + window-size events. Quit handling is
// centralised so q / esc / ctrl-c all reach the same shutdown path.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// --- styles ---

var (
	// Header + footer strips use the same muted border colour so the
	// frame reads as one bounded workspace. Colour choices are
	// terminal-palette-agnostic; lipgloss maps these onto whatever
	// the terminal has available.
	borderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")) // bright cyan for the ww brand

	versionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")) // dim

	welcomeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")) // bright white

	bodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")) // light grey, easy on eyes

	bulletStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")) // dimmer grey for list items

	linkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Underline(true)

	contextLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("246")).
				Bold(true)

	contextValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")) // orange — stands out without shouting

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246"))
)

// View renders the current Model as a string. Bubbletea calls this
// after every Update; the framework handles differential screen
// updates under the hood.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80 // pre-WindowSizeMsg default; avoids zero-width renders on the first frame
	}
	// Clamp inner content to a comfortable max width so the layout
	// doesn't sprawl across ultra-wide terminals.
	inner := width - 4
	if inner > 76 {
		inner = 76
	}
	if inner < 40 {
		inner = 40
	}

	// --- Header strip: "ww · TUI" left, version right ---
	left := titleStyle.Render("ww · TUI")
	right := versionStyle.Render(m.version)
	headerGap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if headerGap < 1 {
		headerGap = 1
	}
	header := left + strings.Repeat(" ", headerGap) + right

	// --- Welcome area ---
	welcome := welcomeStyle.Render("Welcome to ww")

	intro := bodyStyle.Render("The full interactive dashboard is on its way.")

	bullets := strings.Join([]string{
		bulletStyle.Render("  • Live operator status, logs, events"),
		bulletStyle.Render("  • Session drill-down with event stream"),
		bulletStyle.Render("  • Agent send + tail without leaving the terminal"),
	}, "\n")

	followAlong := bodyStyle.Render("Follow along → ") +
		linkStyle.Render("https://github.com/skthomasjr/witwave/issues/1450")

	// --- Cluster context block (or degraded message) ---
	var contextBlock string
	if m.target != nil && m.contextErr == "" {
		contextBlock = renderContextBlock(m.target)
	} else {
		msg := m.contextErr
		if msg == "" {
			msg = "No cluster configured — set $KUBECONFIG or pass --kubeconfig to ww tui."
		}
		contextBlock = warnStyle.Render(msg)
	}

	// --- Footer strip: keybindings ---
	footer := footerStyle.Render("q / esc / ctrl-c — quit")

	// --- Compose ---
	//
	// Vertical stack with centered blocks. lipgloss.PlaceHorizontal
	// centers each piece inside `inner`; JoinVertical stacks them.
	content := lipgloss.JoinVertical(
		lipgloss.Center,
		"",
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, welcome),
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, intro),
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, bullets),
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, followAlong),
		"",
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, contextBlock),
		"",
	)

	// Box everything in a rounded border; the border colour matches
	// the header/footer muted palette so the frame doesn't shout.
	framed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Width(inner).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			borderStyle.Render(strings.Repeat("─", inner)),
			content,
			borderStyle.Render(strings.Repeat("─", inner)),
			lipgloss.PlaceHorizontal(inner, lipgloss.Center, footer),
		))

	// Center the whole frame in the viewport.
	if m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, framed)
	}
	return framed
}

// renderContextBlock formats the three kubeconfig fields as aligned
// label-value rows so they read as a coherent unit.
func renderContextBlock(t *k8s.Target) string {
	cluster := t.Cluster
	if cluster == "" {
		cluster = t.Server
	}
	if cluster == "" {
		cluster = "(unknown)"
	}
	ns := t.Namespace
	if ns == "" {
		ns = "default"
	}
	rows := []string{
		fmt.Sprintf("%s  %s",
			contextLabelStyle.Render("Target cluster:"),
			contextValueStyle.Render(cluster),
		),
		fmt.Sprintf("%s         %s",
			contextLabelStyle.Render("Context:"),
			contextValueStyle.Render(t.Context),
		),
		fmt.Sprintf("%s       %s",
			contextLabelStyle.Render("Namespace:"),
			contextValueStyle.Render(ns),
		),
	}
	return strings.Join(rows, "\n")
}
