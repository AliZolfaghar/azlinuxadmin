package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/fail2ban"
)

type f2bMode int

const (
	f2bModeMain f2bMode = iota
	f2bModeConfig
	f2bModeEdit
	f2bModeConfirm
	f2bModeHistory
)

type f2bConfirm int

const (
	f2bConfirmNone f2bConfirm = iota
	f2bConfirmInstall
	f2bConfirmApply
	f2bConfirmStart
	f2bConfirmStop
	f2bConfirmUndo
)

type f2bPage struct {
	journal *change.Journal
	snap    fail2ban.Snapshot
	history []change.Entry

	mode       f2bMode
	cfgIdx     int
	historyIdx int
	draft      fail2ban.Config

	input    textinput.Model
	editKey  string
	viewport viewport.Model
	ready    bool

	status  string
	errMsg  string
	confirm f2bConfirm
	undoID  string
	loaded  bool
	width   int
	height  int
}

type f2bTickMsg time.Time

type f2bRefreshMsg struct {
	snap    fail2ban.Snapshot
	history []change.Entry
	err     error
}

func newF2BPage() f2bPage {
	ti := textinput.New()
	ti.CharLimit = 128
	ti.Width = 40
	ti.Prompt = "› "
	j, _ := change.Open()
	return f2bPage{
		journal: j,
		input:   ti,
		draft:   fail2ban.DefaultConfig(),
	}
}

func (p f2bPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		snap := fail2ban.LoadSnapshot(120)
		var hist []change.Entry
		var err error
		if p.journal != nil {
			hist, err = p.journal.List()
		}
		var filtered []change.Entry
		for _, e := range hist {
			if strings.HasPrefix(e.Kind, "fail2ban.") {
				filtered = append(filtered, e)
			}
		}
		return f2bRefreshMsg{snap: snap, history: filtered, err: err}
	}
}

func f2bTickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return f2bTickMsg(t)
	})
}

func (p f2bPage) Init() tea.Cmd {
	return tea.Batch(p.refreshCmd(), f2bTickCmd())
}

func (p *f2bPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case f2bRefreshMsg:
		p.loaded = true
		p.snap = msg.snap
		p.history = msg.history
		if msg.err != nil {
			p.errMsg = msg.err.Error()
		}
		if p.mode == f2bModeMain || p.mode == f2bModeConfig {
			p.draft = p.snap.Config
		}
		p.updateViewport()
		return nil

	case f2bTickMsg:
		// Auto-refresh log while viewing main page.
		if p.mode == f2bModeMain {
			return tea.Batch(p.refreshCmd(), f2bTickCmd())
		}
		return f2bTickCmd()
	}

	if p.mode == f2bModeEdit {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				p.input.Blur()
				p.mode = f2bModeConfig
				return nil
			case "enter":
				val := strings.TrimSpace(p.input.Value())
				switch p.editKey {
				case "bantime":
					p.draft.Bantime = val
				case "findtime":
					p.draft.Findtime = val
				case "maxretry":
					n, err := strconv.Atoi(val)
					if err != nil {
						p.errMsg = "invalid maxretry"
						return nil
					}
					p.draft.MaxRetry = n
				case "ignoreip":
					p.draft.IgnoreIP = val
				}
				p.input.Blur()
				p.mode = f2bModeConfig
				p.status = p.editKey + " staged — press a to apply"
				return nil
			}
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if p.mode == f2bModeMain {
			var cmd tea.Cmd
			p.viewport, cmd = p.viewport.Update(msg)
			return cmd
		}
		return nil
	}

	if p.mode == f2bModeConfirm {
		switch km.String() {
		case "y", "Y", "enter":
			return p.runConfirmed()
		case "n", "N", "esc":
			p.confirm = f2bConfirmNone
			p.mode = f2bModeMain
			p.status = "cancelled"
		}
		return nil
	}

	if p.mode == f2bModeHistory {
		switch km.String() {
		case "esc":
			p.mode = f2bModeMain
		case "up", "k":
			if p.historyIdx > 0 {
				p.historyIdx--
			}
		case "down", "j":
			if p.historyIdx < len(p.history)-1 {
				p.historyIdx++
			}
		case "u", "enter":
			if len(p.history) == 0 {
				return nil
			}
			e := p.history[p.historyIdx]
			if e.Undone {
				p.errMsg = "already undone"
				return nil
			}
			p.undoID = e.ID
			p.confirm = f2bConfirmUndo
			p.mode = f2bModeConfirm
			p.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
		}
		return nil
	}

	if p.mode == f2bModeConfig {
		switch km.String() {
		case "esc":
			p.mode = f2bModeMain
			p.draft = p.snap.Config
		case "up", "k":
			if p.cfgIdx > 0 {
				p.cfgIdx--
			}
		case "down", "j":
			if p.cfgIdx < 4 {
				p.cfgIdx++
			}
		case "enter", " ":
			return p.toggleOrEditConfig()
		case "a":
			p.confirm = f2bConfirmApply
			p.mode = f2bModeConfirm
			p.status = "Apply Fail2Ban config — y confirm / n cancel"
		case "c":
			p.draft = p.snap.Config
			p.status = "draft reset"
		}
		return nil
	}

	// Main mode: log viewport + actions
	switch km.String() {
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		p.viewport, cmd = p.viewport.Update(msg)
		return cmd
	case "i":
		if p.snap.Installed {
			p.errMsg = "already installed"
			return nil
		}
		p.confirm = f2bConfirmInstall
		p.mode = f2bModeConfirm
		p.status = "Install fail2ban via apt — y confirm / n cancel"
	case "s":
		if !p.snap.Installed {
			p.errMsg = "install fail2ban first"
			return nil
		}
		if p.snap.Running {
			p.confirm = f2bConfirmStop
			p.status = "Stop fail2ban — y confirm / n cancel"
		} else {
			p.confirm = f2bConfirmStart
			p.status = "Start fail2ban — y confirm / n cancel"
		}
		p.mode = f2bModeConfirm
	case "c":
		if !p.snap.Installed {
			p.errMsg = "install fail2ban first"
			return nil
		}
		p.mode = f2bModeConfig
		p.draft = p.snap.Config
		p.cfgIdx = 0
		p.status = "configure — Enter change, a apply, Esc back"
	case "h":
		p.mode = f2bModeHistory
		p.historyIdx = 0
		p.status = "history — u/Enter undo, Esc back"
	case "r":
		p.status = "refreshing…"
		return p.refreshCmd()
	case "u":
		if p.journal == nil {
			p.errMsg = "journal unavailable"
			return nil
		}
		e, err := p.journal.LastActive("fail2ban.")
		if err != nil || e == nil {
			p.errMsg = "no undoable fail2ban change"
			return nil
		}
		p.undoID = e.ID
		p.confirm = f2bConfirmUndo
		p.mode = f2bModeConfirm
		p.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
	case "g":
		p.viewport.GotoBottom()
	}
	return nil
}

func (p *f2bPage) toggleOrEditConfig() tea.Cmd {
	p.errMsg = ""
	switch p.cfgIdx {
	case 0:
		p.draft.SSHEnabled = !p.draft.SSHEnabled
		p.status = fmt.Sprintf("sshd jail=%v staged — press a to apply", p.draft.SSHEnabled)
	case 1:
		return p.beginEdit("bantime", p.draft.Bantime)
	case 2:
		return p.beginEdit("findtime", p.draft.Findtime)
	case 3:
		return p.beginEdit("maxretry", strconv.Itoa(p.draft.MaxRetry))
	case 4:
		return p.beginEdit("ignoreip", p.draft.IgnoreIP)
	}
	return nil
}

func (p *f2bPage) beginEdit(key, val string) tea.Cmd {
	p.mode = f2bModeEdit
	p.editKey = key
	p.input.SetValue(val)
	p.input.CursorEnd()
	p.input.Focus()
	p.status = "edit " + key + " — Enter save, Esc cancel"
	return nil
}

func (p *f2bPage) runConfirmed() tea.Cmd {
	action := p.confirm
	p.confirm = f2bConfirmNone
	p.mode = f2bModeMain
	if p.journal == nil {
		p.errMsg = "journal unavailable"
		return nil
	}

	var err error
	switch action {
	case f2bConfirmInstall:
		_, err = fail2ban.Install(p.journal)
	case f2bConfirmApply:
		_, err = fail2ban.ApplyConfig(p.journal, p.draft)
	case f2bConfirmStart:
		_, err = fail2ban.SetRunning(p.journal, true)
	case f2bConfirmStop:
		_, err = fail2ban.SetRunning(p.journal, false)
	case f2bConfirmUndo:
		_, err = fail2ban.UndoChange(p.journal, p.undoID)
	}
	if err != nil {
		p.errMsg = err.Error()
		return nil
	}
	switch action {
	case f2bConfirmInstall:
		p.status = "fail2ban installed (undo with u)"
	case f2bConfirmApply:
		p.status = "config applied (undo with u)"
	case f2bConfirmStart:
		p.status = "fail2ban started"
	case f2bConfirmStop:
		p.status = "fail2ban stopped"
	case f2bConfirmUndo:
		p.status = "change undone"
		p.undoID = ""
	}
	return p.refreshCmd()
}

func (p *f2bPage) updateViewport() {
	if !p.ready {
		return
	}
	atBottom := p.viewport.AtBottom()
	p.viewport.SetContent(p.snap.LogTail)
	if atBottom {
		p.viewport.GotoBottom()
	}
}

func (p *f2bPage) View(width, height int) string {
	p.SetSize(width, height)
	var b strings.Builder
	b.WriteString(activeItemStyle.Render("Fail2Ban") + mutedStyle.Render("  (intrusion prevention)") + "\n")
	if !p.loaded {
		b.WriteString(mutedStyle.Render("loading…") + "\n")
		return padBlock(strings.Split(b.String(), "\n"), width, height)
	}
	if os.Geteuid() != 0 {
		b.WriteString(errorStyle.Render("must run as root (sudo)") + "\n")
	}
	if p.snap.Warning != "" {
		b.WriteString(mutedStyle.Render("⚠ "+p.snap.Warning) + "\n")
	}
	if p.errMsg != "" {
		b.WriteString(errorStyle.Render("error: "+p.errMsg) + "\n")
	}
	if p.status != "" {
		b.WriteString(mutedStyle.Render(p.status) + "\n")
	}

	switch p.mode {
	case f2bModeEdit:
		b.WriteString(fmt.Sprintf("\n%s:\n", p.editKey))
		b.WriteString(p.input.View() + "\n")
		return padBlock(strings.Split(b.String(), "\n"), width, height)

	case f2bModeConfirm:
		b.WriteString("\n" + activeItemStyle.Render("Confirm") + "\n\n")
		b.WriteString(p.status + "\n")
		return padBlock(strings.Split(b.String(), "\n"), width, height)

	case f2bModeHistory:
		b.WriteString("\nFail2Ban change history:\n")
		if len(p.history) == 0 {
			b.WriteString(mutedStyle.Render("  (empty)") + "\n")
		}
		for i, e := range p.history {
			cur := "  "
			style := itemStyle
			if i == p.historyIdx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			mark := ""
			if e.Undone {
				mark = " [undone]"
			}
			line := fmt.Sprintf("%s%s  %s%s", cur, e.Time.Local().Format("01-02 15:04"), e.Summary, mark)
			b.WriteString(style.Render(line) + "\n")
		}
		return padBlock(strings.Split(b.String(), "\n"), width, height)

	case f2bModeConfig:
		b.WriteString("\n")
		rows := []struct{ label, value string }{
			{"SSH jail (sshd)", map[bool]string{true: "enabled", false: "disabled"}[p.draft.SSHEnabled]},
			{"Bantime", p.draft.Bantime},
			{"Findtime", p.draft.Findtime},
			{"MaxRetry", strconv.Itoa(p.draft.MaxRetry)},
			{"IgnoreIP", p.draft.IgnoreIP},
		}
		for i, row := range rows {
			cur := "  "
			style := itemStyle
			if i == p.cfgIdx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			b.WriteString(style.Render(fmt.Sprintf("%s%-16s %s", cur, row.label, row.value)) + "\n")
		}
		b.WriteString("\n" + mutedStyle.Render("Enter change · a apply · c reset · Esc back") + "\n")
		return padBlock(strings.Split(b.String(), "\n"), width, height)
	}

	inst := "not installed"
	if p.snap.Installed {
		inst = "installed"
		if p.snap.Version != "" {
			inst += " " + p.snap.Version
		}
	}
	run := "stopped"
	if p.snap.Running {
		run = "running"
	}
	b.WriteString(fmt.Sprintf("\n  status: %s  ·  service: %s\n", inst, run))
	if p.snap.Installed {
		b.WriteString(fmt.Sprintf("  ssh jail: %v  bantime=%s  maxretry=%d\n",
			p.snap.Config.SSHEnabled, p.snap.Config.Bantime, p.snap.Config.MaxRetry))
		if banned := fail2ban.BannedIPs(); len(banned) > 0 {
			b.WriteString("  banned: " + strings.Join(banned, ", ") + "\n")
		}
	}
	b.WriteString(mutedStyle.Render("i install · s start/stop · c config · h history · r refresh · g end · u undo") + "\n")
	b.WriteString(mutedStyle.Render("── log (/var/log/fail2ban.log) ──") + "\n")

	header := b.String()
	headerLines := strings.Count(header, "\n")
	logH := height - headerLines
	if logH < 5 {
		logH = 5
	}
	p.viewport.Width = width
	p.viewport.Height = logH
	p.updateViewport()

	out := header + p.viewport.View()
	return padBlock(strings.Split(out, "\n"), width, height)
}

// SetSize updates viewport dimensions from parent layout.
func (p *f2bPage) SetSize(width, height int) {
	p.width, p.height = width, height
	logH := height - 10
	if logH < 5 {
		logH = 5
	}
	if !p.ready {
		p.viewport = viewport.New(width, logH)
		p.ready = true
		p.viewport.SetContent(p.snap.LogTail)
		p.viewport.GotoBottom()
		return
	}
	p.viewport.Width = width
	p.viewport.Height = logH
}
