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

type focusArea int

const (
	focusSidebar focusArea = iota
	focusMain
)

// Model is the root Bubbletea model for the shell UI.
type Model struct {
	width    int
	height   int
	cursor   int
	focus    focusArea
	host     string
	username string
	hasSudo  bool

	network networkPage
	users   usersPage
	ssh     sshPage
	f2b     f2bPage
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
		focus:    focusSidebar,
		network:  newNetworkPage(),
		users:    newUsersPage(),
		ssh:      newSSHPage(),
		f2b:      newF2BPage(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.network.Init(), m.users.Init(), m.ssh.Init(), m.f2b.Init())
}

func (m Model) activeModule() string {
	if m.focus != focusMain {
		return ""
	}
	return menuItems[m.cursor]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case netRefreshMsg:
		cmd := m.network.Update(msg)
		if m.network.snap.Hostname != "" {
			m.host = m.network.snap.Hostname
		}
		return m, cmd

	case usersRefreshMsg:
		return m, m.users.Update(msg)

	case sshRefreshMsg:
		return m, m.ssh.Update(msg)

	case f2bRefreshMsg, f2bTickMsg:
		return m, m.f2b.Update(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		switch m.activeModule() {
		case "Network":
			if msg.String() == "esc" && m.network.mode == netModeMenu {
				m.focus = focusSidebar
				return m, nil
			}
			if msg.String() == "q" && m.network.mode == netModeMenu {
				m.focus = focusSidebar
				return m, nil
			}
			cmd := m.network.Update(msg)
			if m.network.snap.Hostname != "" {
				m.host = m.network.snap.Hostname
			}
			return m, cmd

		case "Users":
			if msg.String() == "esc" && m.users.mode == usersModeList {
				m.focus = focusSidebar
				return m, nil
			}
			if msg.String() == "q" && m.users.mode == usersModeList {
				m.focus = focusSidebar
				return m, nil
			}
			return m, m.users.Update(msg)

		case "SSH":
			if msg.String() == "esc" && m.ssh.mode == sshModeList {
				m.focus = focusSidebar
				return m, nil
			}
			if msg.String() == "q" && m.ssh.mode == sshModeList {
				m.focus = focusSidebar
				return m, nil
			}
			return m, m.ssh.Update(msg)

		case "Fail2Ban":
			if msg.String() == "esc" && m.f2b.mode == f2bModeMain {
				m.focus = focusSidebar
				return m, nil
			}
			if msg.String() == "q" && m.f2b.mode == f2bModeMain {
				m.focus = focusSidebar
				return m, nil
			}
			return m, m.f2b.Update(msg)
		}

		switch msg.String() {
		case "q":
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
			if menuItems[m.cursor] == "Network" {
				m.focus = focusMain
				m.network.mode = netModeMenu
				m.network.status = ""
				m.network.errMsg = ""
				return m, m.network.refreshCmd()
			}
			if menuItems[m.cursor] == "Users" {
				m.focus = focusMain
				m.users.mode = usersModeList
				m.users.status = ""
				m.users.errMsg = ""
				return m, m.users.refreshCmd()
			}
			if menuItems[m.cursor] == "SSH" {
				m.focus = focusMain
				m.ssh.mode = sshModeList
				m.ssh.status = ""
				m.ssh.errMsg = ""
				m.ssh.draft = map[string]string{}
				return m, m.ssh.refreshCmd()
			}
			if menuItems[m.cursor] == "Fail2Ban" {
				m.focus = focusMain
				m.f2b.mode = f2bModeMain
				m.f2b.status = ""
				m.f2b.errMsg = ""
				return m, tea.Batch(m.f2b.refreshCmd(), f2bTickCmd())
			}
		case "/", "?":
			// later
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small (%dx%d). Need at least %dx%d.", m.width, m.height, minWidth, minHeight)
	}

	headerH := 1
	footerH := 1
	bodyH := m.height - headerH - footerH - 4
	if bodyH < 3 {
		bodyH = 3
	}

	sideW := sidebarWidth
	mainW := m.width - sideW - 3
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
		active := i == m.cursor
		if active {
			cursor = cursorStyle.Render("▸") + " "
			style = activeItemStyle
		}
		label := style.Render(cursor + item)
		if active && m.focus == focusMain {
			label = style.Render(cursor+item) + mutedStyle.Render(" •")
		}
		lines = append(lines, label)
	}
	return padBlock(lines, width, height)
}

func (m Model) renderMain(width, height int) string {
	switch menuItems[m.cursor] {
	case "Network":
		return m.network.View(width, height)
	case "Users":
		return m.users.View(width, height)
	case "SSH":
		return m.ssh.View(width, height)
	case "Fail2Ban":
		return m.f2b.View(width, height)
	}
	label := mutedStyle.Italic(true).Render(fmt.Sprintf("%s — coming soon", menuItems[m.cursor]))
	hint := ""
	if m.focus == focusSidebar {
		hint = mutedStyle.Render("Enter to open when available")
	}
	lines := []string{"", " " + label, "", " " + hint}
	return padBlock(lines, width, height)
}

func (m Model) renderFooter(innerWidth int) string {
	var parts []string
	switch {
	case m.focus == focusMain && menuItems[m.cursor] == "Network":
		parts = []string{
			keyStyle.Render("↑↓") + mutedStyle.Render(" : move"),
			keyStyle.Render("Enter") + mutedStyle.Render(" : select"),
			keyStyle.Render("r") + mutedStyle.Render(" : refresh"),
			keyStyle.Render("u") + mutedStyle.Render(" : undo"),
			keyStyle.Render("Esc") + mutedStyle.Render(" : sidebar"),
		}
	case m.focus == focusMain && menuItems[m.cursor] == "Users":
		parts = []string{
			keyStyle.Render("a") + mutedStyle.Render(" : add"),
			keyStyle.Render("e") + mutedStyle.Render(" : edit"),
			keyStyle.Render("d") + mutedStyle.Render(" : del"),
			keyStyle.Render("x") + mutedStyle.Render(" : disable"),
			keyStyle.Render("u") + mutedStyle.Render(" : undo"),
			keyStyle.Render("Esc") + mutedStyle.Render(" : sidebar"),
		}
	case m.focus == focusMain && menuItems[m.cursor] == "SSH":
		parts = []string{
			keyStyle.Render("Enter") + mutedStyle.Render(" : change"),
			keyStyle.Render("a") + mutedStyle.Render(" : apply"),
			keyStyle.Render("c") + mutedStyle.Render(" : clear"),
			keyStyle.Render("u") + mutedStyle.Render(" : undo"),
			keyStyle.Render("Esc") + mutedStyle.Render(" : sidebar"),
		}
	case m.focus == focusMain && menuItems[m.cursor] == "Fail2Ban":
		parts = []string{
			keyStyle.Render("i") + mutedStyle.Render(" : install"),
			keyStyle.Render("c") + mutedStyle.Render(" : config"),
			keyStyle.Render("s") + mutedStyle.Render(" : start/stop"),
			keyStyle.Render("u") + mutedStyle.Render(" : undo"),
			keyStyle.Render("Esc") + mutedStyle.Render(" : sidebar"),
		}
	default:
		parts = []string{
			keyStyle.Render("↑↓") + mutedStyle.Render(" : move"),
			keyStyle.Render("Enter") + mutedStyle.Render(" : select"),
			keyStyle.Render("/") + mutedStyle.Render(" : search"),
			keyStyle.Render("?") + mutedStyle.Render(" : help"),
			keyStyle.Render("q") + mutedStyle.Render(" : exit"),
		}
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
