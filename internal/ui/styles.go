package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.Color("39")
	colorMuted  = lipgloss.Color("245")
	colorOk     = lipgloss.Color("42")
	colorBorder = lipgloss.Color("240")

	mutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	okStyle    = lipgloss.NewStyle().Foreground(colorOk).Bold(true)
	keyStyle   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	itemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	activeItemStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)
	cursorStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	headerStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, true, false, true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, true, true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	mainStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, true, true, true).
			BorderForeground(colorBorder).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, true, true).
			BorderForeground(colorBorder).
			Padding(0, 1)
)
