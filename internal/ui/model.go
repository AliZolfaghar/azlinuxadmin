package ui

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const appVersion = "v0.1"

var menuItems = []string{
	"Dashboard",
	"Users",
	"SSH",
	"Firewall",
	"Fail2Ban",
	"Services",
	"Network",
	"Packages",
	"Settings",
	"Exit",
}

const (
	sidebarWidth = 16
	minWidth     = 60
	minHeight    = 16
)

// Model is the root Bubbletea model for the shell UI.
type Model struct {
	width    int
	height   int
	cursor   int
	host     string
	username string
	hasSudo  bool
}

// New creates the initial UI model with host/user metadata.
func New() Model {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}

	username := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	}

	return Model{
		host:     host,
		username: username,
		hasSudo:  os.Geteuid() == 0,
		cursor:   0,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(menuItems) - 1
			}
		case "down", "j":
			if m.cursor < len(menuItems)-1 {
				m.cursor++
			} else {
				m.cursor = 0
			}
		case "enter":
			if menuItems[m.cursor] == "Exit" {
				return m, tea.Quit
			}
			// Modules come later — Enter is a no-op for now.
		case "/", "?":
			// Search/help come later.
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small (%dx%d). Need at least %dx%d.", m.width, m.height, minWidth, minHeight)
	}

	// Frame: header / (sidebar | main) / footer, matching the ASCII mockup.
	headerH := 1
	footerH := 1
	// +4 for three horizontal border rows and content padding accounting
	bodyH := m.height - headerH - footerH - 4
	if bodyH < 3 {
		bodyH = 3
	}

	sideW := sidebarWidth
	mainW := m.width - sideW - 3 // vertical borders: left, mid, right
	if mainW < 20 {
		mainW = 20
		sideW = m.width - mainW - 3
	}

	header := m.renderHeader(m.width - 2)
	sidebar := m.renderSidebar(sideW, bodyH)
	main := m.renderMain(mainW, bodyH)
	footer := m.renderFooter(m.width - 2)

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		sidebarStyle.Width(sideW).Height(bodyH).Render(sidebar),
		mainStyle.Width(mainW).Height(bodyH).Render(main),
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Width(m.width-2).Render(header),
		body,
		footerStyle.Width(m.width-2).Render(footer),
	)
}

func (m Model) renderHeader(innerWidth int) string {
	sudoMark := "✗"
	if m.hasSudo {
		sudoMark = "✓"
	}

	title := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("🐧 AZLinuxAdmin %s", appVersion))
	sep := mutedStyle.Render("  |  ")
	host := mutedStyle.Render(fmt.Sprintf("host: %s", m.host))
	userPart := mutedStyle.Render(fmt.Sprintf("user: %s", m.username))
	sudoLabel := mutedStyle.Render("sudo:")
	sudoVal := okStyle.Render(sudoMark)
	if !m.hasSudo {
		sudoVal = mutedStyle.Render(sudoMark)
	}

	line := lipgloss.JoinHorizontal(lipgloss.Top, title, sep, host, sep, userPart, sep, sudoLabel, sudoVal)
	return padLine(line, innerWidth)
}

func (m Model) renderSidebar(width, height int) string {
	var lines []string
	lines = append(lines, "")
	for i, item := range menuItems {
		cursor := "  "
		style := itemStyle
		if i == m.cursor {
			cursor = cursorStyle.Render("▸") + " "
			style = activeItemStyle
		}
		lines = append(lines, style.Render(cursor+item))
	}
	return padBlock(lines, width, height)
}

func (m Model) renderMain(width, height int) string {
	label := mutedStyle.Italic(true).Render(fmt.Sprintf("%s — coming soon", menuItems[m.cursor]))
	lines := []string{"", " " + label}
	return padBlock(lines, width, height)
}

func (m Model) renderFooter(innerWidth int) string {
	parts := []string{
		keyStyle.Render("↑↓") + mutedStyle.Render(" : move"),
		keyStyle.Render("Enter") + mutedStyle.Render(" : select"),
		keyStyle.Render("/") + mutedStyle.Render(" : search"),
		keyStyle.Render("?") + mutedStyle.Render(" : help"),
		keyStyle.Render("q") + mutedStyle.Render(" : exit"),
	}
	line := "  " + strings.Join(parts, "   ")
	return padLine(line, innerWidth)
}

func padLine(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return lipgloss.NewStyle().MaxWidth(width).Render(s)
	}
	return s + strings.Repeat(" ", width-w)
}

func padBlock(lines []string, width, height int) string {
	out := make([]string, 0, height)
	for _, line := range lines {
		out = append(out, padLine(line, width))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	if len(out) > height {
		out = out[:height]
	}
	return strings.Join(out, "\n")
}
