package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

// needPrivMsg asks the root model to collect a sudo password then retry.
type needPrivMsg struct {
	Reason string
	Retry  tea.Cmd
}

type sudoModal struct {
	active   bool
	reason   string
	remember bool
	input    textinput.Model
	errMsg   string
	retry    tea.Cmd
}

func newSudoModal() sudoModal {
	ti := textinput.New()
	ti.Placeholder = "sudo password"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Width = 32
	ti.Prompt = "› "
	return sudoModal{input: ti}
}

func (s *sudoModal) open(reason string, retry tea.Cmd) {
	s.active = true
	s.reason = reason
	s.retry = retry
	s.errMsg = ""
	s.input.SetValue("")
	s.input.Focus()
}

func (s *sudoModal) close() {
	s.active = false
	s.input.Blur()
	s.input.SetValue("")
	s.errMsg = ""
	s.retry = nil
}

func (s *sudoModal) Update(msg tea.Msg) tea.Cmd {
	if !s.active {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return cmd
	}
	switch km.String() {
	case "esc":
		s.close()
		return nil
	case "tab":
		s.remember = !s.remember
		return nil
	case "ctrl+r":
		s.remember = !s.remember
		return nil
	case "enter":
		pass := s.input.Value()
		remember := s.remember
		retry := s.retry
		if err := priv.Authenticate(pass, remember); err != nil {
			s.errMsg = err.Error()
			s.input.SetValue("")
			return nil
		}
		s.close()
		return retry
	default:
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return cmd
	}
}

func (s sudoModal) View(width, height int) string {
	if !s.active {
		return ""
	}

	boxW := 46
	if width-4 < boxW {
		boxW = width - 4
	}
	if boxW < 30 {
		boxW = 30
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render("sudo authentication")
	reason := mutedStyle.Render(s.reason)
	if reason == "" {
		reason = mutedStyle.Render("Administrator privileges are required")
	}

	check := "[ ]"
	if s.remember {
		check = keyStyle.Render("[x]")
	}
	rememberLine := check + mutedStyle.Render(" Remember password until I exit  (Tab)")

	var body strings.Builder
	body.WriteString(title + "\n\n")
	body.WriteString(reason + "\n\n")
	body.WriteString("Password:\n")
	body.WriteString(s.input.View() + "\n\n")
	body.WriteString(rememberLine + "\n")
	if s.errMsg != "" {
		body.WriteString("\n" + errorStyle.Render(s.errMsg) + "\n")
	}
	body.WriteString("\n" + mutedStyle.Render("Enter confirm · Esc cancel"))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2).
		Width(boxW).
		Render(body.String())

	// Center overlay on a dimmed full-screen canvas.
	canvas := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel,
		lipgloss.WithWhitespaceForeground(lipgloss.Color("236")),
	)
	return canvas
}
