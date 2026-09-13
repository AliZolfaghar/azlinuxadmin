package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliZolfaghar/azlinuxadmin/internal/priv"
)

// privRetryMsg re-runs the pending privileged confirm after sudo auth.
type privRetryMsg struct{}

func askPriv(reason string) tea.Cmd {
	return func() tea.Msg {
		return needPrivMsg{
			Reason: reason,
			Retry: func() tea.Msg {
				return privRetryMsg{}
			},
		}
	}
}

func maybeAskPriv(err error, reason string) tea.Cmd {
	if priv.NeedPassword(err) {
		return askPriv(reason)
	}
	return nil
}
