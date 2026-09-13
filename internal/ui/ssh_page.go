package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/ssh"
)

type sshMode int

const (
	sshModeList sshMode = iota
	sshModeEditInt
	sshModeConfirm
	sshModeHistory
)

type sshConfirm int

const (
	sshConfirmNone sshConfirm = iota
	sshConfirmApply
	sshConfirmUndo
)

type sshPage struct {
	journal *change.Journal
	snap    ssh.Snapshot
	history []change.Entry

	mode       sshMode
	idx        int
	historyIdx int

	// draft overrides (lowercase keys); empty means use effective/current display
	draft map[string]string

	input   textinput.Model
	status  string
	errMsg  string
	confirm sshConfirm
	undoID  string
	loaded  bool
}

func newSSHPage() sshPage {
	ti := textinput.New()
	ti.CharLimit = 16
	ti.Width = 20
	ti.Prompt = "› "
	j, _ := change.Open()
	return sshPage{
		journal: j,
		draft:   map[string]string{},
		input:   ti,
	}
}

type sshRefreshMsg struct {
	snap    ssh.Snapshot
	history []change.Entry
	err     error
}

func (p sshPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		snap := ssh.LoadSnapshot()
		var hist []change.Entry
		var err error
		if p.journal != nil {
			hist, err = p.journal.List()
		}
		var filtered []change.Entry
		for _, e := range hist {
			if strings.HasPrefix(e.Kind, "ssh.") {
				filtered = append(filtered, e)
			}
		}
		return sshRefreshMsg{snap: snap, history: filtered, err: err}
	}
}

func (p sshPage) Init() tea.Cmd {
	return p.refreshCmd()
}

func (p *sshPage) displayValue(key string) string {
	if v, ok := p.draft[key]; ok {
		return v
	}
	if v, ok := p.snap.Values[key]; ok {
		return v
	}
	return "-"
}

func (p *sshPage) dirty() bool {
	return len(p.draft) > 0
}

func (p *sshPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sshRefreshMsg:
		p.loaded = true
		p.snap = msg.snap
		p.history = msg.history
		if msg.err != nil {
			p.errMsg = msg.err.Error()
		}
		return nil
	}

	if p.mode == sshModeEditInt {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				p.input.Blur()
				p.mode = sshModeList
				return nil
			case "enter":
				def := ssh.CommonSettings[p.idx]
				val := strings.TrimSpace(p.input.Value())
				if _, err := strconv.Atoi(val); err != nil {
					p.errMsg = "invalid number"
					return nil
				}
				p.draft[def.Key] = val
				p.input.Blur()
				p.mode = sshModeList
				p.status = def.Label + " staged — press a to apply"
				return nil
			}
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if p.mode == sshModeConfirm {
		switch km.String() {
		case "y", "Y", "enter":
			return p.runConfirmed()
		case "n", "N", "esc":
			p.confirm = sshConfirmNone
			p.mode = sshModeList
			p.status = "cancelled"
		}
		return nil
	}

	if p.mode == sshModeHistory {
		switch km.String() {
		case "esc":
			p.mode = sshModeList
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
			p.confirm = sshConfirmUndo
			p.mode = sshModeConfirm
			p.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
		}
		return nil
	}

	// List mode
	switch km.String() {
	case "up", "k":
		if p.idx > 0 {
			p.idx--
		}
	case "down", "j":
		if p.idx < len(ssh.CommonSettings)-1 {
			p.idx++
		}
	case "enter", " ":
		return p.toggleOrEdit()
	case "a":
		if !p.dirty() {
			p.errMsg = "no staged changes"
			return nil
		}
		p.confirm = sshConfirmApply
		p.mode = sshModeConfirm
		p.status = fmt.Sprintf("Apply %d SSH setting(s)? Wrong values can lock you out — y confirm / n cancel", len(p.draft))
	case "c":
		p.draft = map[string]string{}
		p.status = "draft cleared"
		p.errMsg = ""
	case "h":
		p.mode = sshModeHistory
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
		e, err := p.journal.LastActive("ssh.")
		if err != nil || e == nil {
			p.errMsg = "no undoable SSH change"
			return nil
		}
		p.undoID = e.ID
		p.confirm = sshConfirmUndo
		p.mode = sshModeConfirm
		p.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
	}
	return nil
}

func (p *sshPage) toggleOrEdit() tea.Cmd {
	def := ssh.CommonSettings[p.idx]
	cur := p.displayValue(def.Key)
	p.errMsg = ""
	switch def.Kind {
	case ssh.KindBool:
		next := "yes"
		if cur == "yes" {
			next = "no"
		}
		p.draft[def.Key] = next
		p.status = def.Label + "=" + next + " staged — press a to apply"
	case ssh.KindEnum:
		vals := def.EnumValues
		i := 0
		for j, v := range vals {
			if v == cur {
				i = j
				break
			}
		}
		next := vals[(i+1)%len(vals)]
		p.draft[def.Key] = next
		p.status = def.Label + "=" + next + " staged — press a to apply"
	case ssh.KindInt:
		p.mode = sshModeEditInt
		p.input.SetValue(cur)
		p.input.CursorEnd()
		p.input.Focus()
		p.status = "edit " + def.Label + " — Enter save, Esc cancel"
	}
	return nil
}

func (p *sshPage) runConfirmed() tea.Cmd {
	action := p.confirm
	if p.journal == nil {
		p.confirm = sshConfirmNone
		p.mode = sshModeList
		p.errMsg = "journal unavailable"
		return nil
	}

	var err error
	switch action {
	case sshConfirmApply:
		_, err = ssh.ApplySettings(p.journal, p.draft)
	case sshConfirmUndo:
		_, err = ssh.UndoSSHChange(p.journal, p.undoID)
	default:
		p.confirm = sshConfirmNone
		p.mode = sshModeList
		return nil
	}

	p.confirm = sshConfirmNone
	p.mode = sshModeList
	if err != nil {
		p.errMsg = err.Error()
		return nil
	}
	switch action {
	case sshConfirmApply:
		p.draft = map[string]string{}
		p.status = "SSH settings applied (undo with u)"
	case sshConfirmUndo:
		p.status = "change undone"
		p.undoID = ""
		p.draft = map[string]string{}
	}
	return p.refreshCmd()
}

func (p sshPage) View(width, height int) string {
	var b strings.Builder
	b.WriteString(activeItemStyle.Render("SSH") + mutedStyle.Render("  (sshd_config.d/99-azlinuxadmin.conf)") + "\n")
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
	if p.dirty() && p.mode == sshModeList {
		b.WriteString(keyStyle.Render(fmt.Sprintf("%d staged", len(p.draft))) + mutedStyle.Render(" — a apply · c clear") + "\n")
	}
	b.WriteString("\n")

	switch p.mode {
	case sshModeEditInt:
		def := ssh.CommonSettings[p.idx]
		b.WriteString(fmt.Sprintf("%s:\n", def.Label))
		b.WriteString(p.input.View() + "\n")
		b.WriteString(mutedStyle.Render(def.Description) + "\n")
	case sshModeHistory:
		b.WriteString("SSH change history:\n")
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
	case sshModeConfirm:
		b.WriteString(activeItemStyle.Render("Confirm") + "\n\n")
		b.WriteString(p.status + "\n")
		if p.confirm == sshConfirmApply {
			b.WriteString("\n")
			for _, def := range ssh.CommonSettings {
				if v, ok := p.draft[def.Key]; ok {
					b.WriteString(fmt.Sprintf("  %s = %s\n", def.Label, v))
				}
			}
		}
	default:
		b.WriteString(mutedStyle.Render("Enter/Space change · a apply · c clear · h history · u undo · r refresh") + "\n\n")
		for i, def := range ssh.CommonSettings {
			cur := "  "
			style := itemStyle
			if i == p.idx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			val := p.displayValue(def.Key)
			staged := ""
			if _, ok := p.draft[def.Key]; ok {
				staged = keyStyle.Render(" *")
			}
			line := fmt.Sprintf("%s%-28s %s", cur, def.Label, val)
			b.WriteString(style.Render(line) + staged + "\n")
			if i == p.idx {
				b.WriteString(mutedStyle.Render("    "+def.Description) + "\n")
			}
		}
	}

	return padBlock(strings.Split(b.String(), "\n"), width, height)
}
