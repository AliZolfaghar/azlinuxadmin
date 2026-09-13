package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/users"
)

type usersMode int

const (
	usersModeList usersMode = iota
	usersModeAdd
	usersModeEdit
	usersModeConfirm
	usersModeHistory
)

type usersConfirm int

const (
	usersConfirmNone usersConfirm = iota
	usersConfirmAdd
	usersConfirmEdit
	usersConfirmDelete
	usersConfirmDisable
	usersConfirmEnable
	usersConfirmUndo
)

type usersPage struct {
	journal *change.Journal
	snap    users.Snapshot
	history []change.Entry

	mode       usersMode
	idx        int
	historyIdx int
	showSystem bool
	offset     int

	// form fields for add/edit
	fieldIdx int
	inputs   []textinput.Model

	status  string
	errMsg  string
	confirm usersConfirm
	undoID  string
	loaded  bool
	filter  []int // indices into snap.Accounts for visible list
}

func newUsersPage() usersPage {
	j, _ := change.Open()
	p := usersPage{
		journal: j,
		inputs:  makeUserInputs(),
	}
	return p
}

func makeUserInputs() []textinput.Model {
	// 0 name, 1 password, 2 shell, 3 gecos, 4 home
	labels := []struct {
		placeholder string
		charLimit   int
		password    bool
	}{
		{"username", 32, false},
		{"password (optional)", 128, true},
		{"/bin/bash", 64, false},
		{"Full Name", 64, false},
		{"/home/user", 128, false},
	}
	out := make([]textinput.Model, len(labels))
	for i, l := range labels {
		ti := textinput.New()
		ti.Placeholder = l.placeholder
		ti.CharLimit = l.charLimit
		ti.Width = 36
		ti.Prompt = "› "
		if l.password {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		out[i] = ti
	}
	return out
}

type usersRefreshMsg struct {
	snap    users.Snapshot
	history []change.Entry
	err     error
}

func (p usersPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		snap := users.LoadSnapshot()
		var hist []change.Entry
		var err error
		if p.journal != nil {
			hist, err = p.journal.List()
		}
		var filtered []change.Entry
		for _, e := range hist {
			if strings.HasPrefix(e.Kind, "users.") {
				filtered = append(filtered, e)
			}
		}
		return usersRefreshMsg{snap: snap, history: filtered, err: err}
	}
}

func (p usersPage) Init() tea.Cmd {
	return p.refreshCmd()
}

func (p *usersPage) rebuildFilter() {
	p.filter = p.filter[:0]
	for i, a := range p.snap.Accounts {
		if !p.showSystem && a.IsSystem {
			continue
		}
		p.filter = append(p.filter, i)
	}
	if p.idx >= len(p.filter) {
		p.idx = len(p.filter) - 1
	}
	if p.idx < 0 {
		p.idx = 0
	}
}

func (p *usersPage) selected() *users.Account {
	if len(p.filter) == 0 || p.idx < 0 || p.idx >= len(p.filter) {
		return nil
	}
	return &p.snap.Accounts[p.filter[p.idx]]
}

func (p *usersPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case usersRefreshMsg:
		p.loaded = true
		p.snap = msg.snap
		p.history = msg.history
		if msg.err != nil {
			p.errMsg = msg.err.Error()
		}
		p.rebuildFilter()
		return nil
	}

	switch p.mode {
	case usersModeAdd, usersModeEdit:
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				p.blurForm()
				p.mode = usersModeList
				return nil
			case "tab", "down":
				p.fieldIdx = (p.fieldIdx + 1) % len(p.inputs)
				if p.mode == usersModeEdit && p.fieldIdx == 0 {
					p.fieldIdx = 1
				}
				p.focusField()
				return nil
			case "shift+tab", "up":
				p.fieldIdx = (p.fieldIdx - 1 + len(p.inputs)) % len(p.inputs)
				if p.mode == usersModeEdit && p.fieldIdx == 0 {
					p.fieldIdx = len(p.inputs) - 1
				}
				p.focusField()
				return nil
			case "enter":
				return p.submitForm()
			}
		}
		var cmd tea.Cmd
		p.inputs[p.fieldIdx], cmd = p.inputs[p.fieldIdx].Update(msg)
		return cmd
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if p.mode == usersModeConfirm {
		switch km.String() {
		case "y", "Y", "enter":
			return p.runConfirmed()
		case "n", "N", "esc":
			p.confirm = usersConfirmNone
			p.mode = usersModeList
			p.status = "cancelled"
		}
		return nil
	}

	if p.mode == usersModeHistory {
		switch km.String() {
		case "esc":
			p.mode = usersModeList
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
			p.confirm = usersConfirmUndo
			p.mode = usersModeConfirm
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
		if p.idx < len(p.filter)-1 {
			p.idx++
		}
	case "a":
		p.beginAdd()
	case "e", "enter":
		if p.selected() != nil {
			p.beginEdit()
		}
	case "d":
		acct := p.selected()
		if acct == nil {
			return nil
		}
		if acct.Name == "root" {
			p.errMsg = "refusing to delete root"
			return nil
		}
		p.confirm = usersConfirmDelete
		p.mode = usersModeConfirm
		p.status = fmt.Sprintf("Delete user %q and home? — y confirm / n cancel", acct.Name)
	case "x":
		acct := p.selected()
		if acct == nil {
			return nil
		}
		if acct.Name == "root" && !acct.Locked {
			p.errMsg = "refusing to disable root"
			return nil
		}
		if acct.Locked {
			p.confirm = usersConfirmEnable
			p.status = fmt.Sprintf("Enable user %q — y confirm / n cancel", acct.Name)
		} else {
			p.confirm = usersConfirmDisable
			p.status = fmt.Sprintf("Disable user %q — y confirm / n cancel", acct.Name)
		}
		p.mode = usersModeConfirm
	case "s":
		p.showSystem = !p.showSystem
		p.rebuildFilter()
		p.status = map[bool]string{true: "showing system users", false: "hiding system users"}[p.showSystem]
	case "h":
		p.mode = usersModeHistory
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
		e, err := p.journal.LastActive("users.")
		if err != nil || e == nil {
			p.errMsg = "no undoable user change"
			return nil
		}
		p.undoID = e.ID
		p.confirm = usersConfirmUndo
		p.mode = usersModeConfirm
		p.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
	}
	return nil
}

func (p *usersPage) beginAdd() {
	p.mode = usersModeAdd
	p.errMsg = ""
	p.inputs = makeUserInputs()
	p.inputs[2].SetValue("/bin/bash")
	p.fieldIdx = 0
	p.focusField()
	p.status = "add user — Tab fields, Enter apply, Esc cancel"
}

func (p *usersPage) beginEdit() {
	acct := p.selected()
	if acct == nil {
		return
	}
	p.mode = usersModeEdit
	p.errMsg = ""
	p.inputs = makeUserInputs()
	p.inputs[0].SetValue(acct.Name)
	p.inputs[0].Blur()
	// name not editable
	p.inputs[1].SetValue("")
	p.inputs[1].Placeholder = "new password (leave empty to keep)"
	p.inputs[2].SetValue(acct.Shell)
	p.inputs[3].SetValue(acct.GECOS)
	p.inputs[4].SetValue(acct.Home)
	p.fieldIdx = 1
	p.focusField()
	p.status = fmt.Sprintf("edit %s — Tab fields, Enter apply, Esc cancel", acct.Name)
}

func (p *usersPage) focusField() {
	if p.mode == usersModeEdit && p.fieldIdx == 0 {
		p.fieldIdx = 1
	}
	for i := range p.inputs {
		if i == p.fieldIdx {
			p.inputs[i].Focus()
		} else {
			p.inputs[i].Blur()
		}
	}
}

func (p *usersPage) blurForm() {
	for i := range p.inputs {
		p.inputs[i].Blur()
	}
}

func (p *usersPage) submitForm() tea.Cmd {
	p.blurForm()
	if p.mode == usersModeAdd {
		p.confirm = usersConfirmAdd
		p.status = fmt.Sprintf("Create user %q — y confirm / n cancel", strings.TrimSpace(p.inputs[0].Value()))
	} else {
		p.confirm = usersConfirmEdit
		p.status = fmt.Sprintf("Save changes to %q — y confirm / n cancel", strings.TrimSpace(p.inputs[0].Value()))
	}
	p.mode = usersModeConfirm
	return nil
}

func (p *usersPage) runConfirmed() tea.Cmd {
	action := p.confirm
	if p.journal == nil {
		p.confirm = usersConfirmNone
		p.mode = usersModeList
		p.errMsg = "journal unavailable"
		return nil
	}

	var err error
	switch action {
	case usersConfirmAdd:
		_, err = users.AddUser(p.journal, users.AddRequest{
			Name:       strings.TrimSpace(p.inputs[0].Value()),
			Password:   p.inputs[1].Value(),
			Shell:      strings.TrimSpace(p.inputs[2].Value()),
			GECOS:      strings.TrimSpace(p.inputs[3].Value()),
			Home:       strings.TrimSpace(p.inputs[4].Value()),
			CreateHome: true,
		})
	case usersConfirmEdit:
		_, err = users.EditUser(p.journal, users.EditRequest{
			Name:     strings.TrimSpace(p.inputs[0].Value()),
			Password: p.inputs[1].Value(),
			Shell:    strings.TrimSpace(p.inputs[2].Value()),
			GECOS:    strings.TrimSpace(p.inputs[3].Value()),
			Home:     strings.TrimSpace(p.inputs[4].Value()),
		})
	case usersConfirmDelete:
		acct := p.selected()
		if acct == nil {
			p.confirm = usersConfirmNone
			p.mode = usersModeList
			return nil
		}
		_, err = users.DeleteUser(p.journal, acct.Name, true)
	case usersConfirmDisable:
		acct := p.selected()
		if acct == nil {
			p.confirm = usersConfirmNone
			p.mode = usersModeList
			return nil
		}
		_, err = users.SetDisabled(p.journal, acct.Name, true)
	case usersConfirmEnable:
		acct := p.selected()
		if acct == nil {
			p.confirm = usersConfirmNone
			p.mode = usersModeList
			return nil
		}
		_, err = users.SetDisabled(p.journal, acct.Name, false)
	case usersConfirmUndo:
		_, err = users.UndoUserChange(p.journal, p.undoID)
	default:
		p.confirm = usersConfirmNone
		p.mode = usersModeList
		return nil
	}

	p.confirm = usersConfirmNone
	p.mode = usersModeList
	if err != nil {
		p.errMsg = err.Error()
		return nil
	}
	switch action {
	case usersConfirmAdd:
		p.status = "user created (undo with u)"
	case usersConfirmEdit:
		p.status = "user updated (undo with u)"
	case usersConfirmDelete:
		p.status = "user deleted (undo with u)"
	case usersConfirmDisable:
		p.status = "user disabled (undo with u)"
	case usersConfirmEnable:
		p.status = "user enabled (undo with u)"
	case usersConfirmUndo:
		p.status = "change undone"
		p.undoID = ""
	}
	return p.refreshCmd()
}

func (p usersPage) View(width, height int) string {
	var b strings.Builder
	b.WriteString(activeItemStyle.Render("Users") + mutedStyle.Render("  (local accounts)") + "\n")
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
	b.WriteString("\n")

	switch p.mode {
	case usersModeAdd, usersModeEdit:
		title := "Add user"
		if p.mode == usersModeEdit {
			title = "Edit user"
		}
		b.WriteString(activeItemStyle.Render(title) + "\n")
		labels := []string{"Username", "Password", "Shell", "Full name", "Home"}
		for i, label := range labels {
			cur := "  "
			style := itemStyle
			if i == p.fieldIdx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			disabled := p.mode == usersModeEdit && i == 0
			line := style.Render(cur+label) + "\n"
			b.WriteString(line)
			if disabled {
				b.WriteString(mutedStyle.Render("  "+p.inputs[i].Value()) + "\n")
			} else {
				b.WriteString("  " + p.inputs[i].View() + "\n")
			}
		}
	case usersModeHistory:
		b.WriteString("User change history:\n")
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
	case usersModeConfirm:
		b.WriteString(activeItemStyle.Render("Confirm") + "\n\n")
		b.WriteString(p.status + "\n")
	default:
		b.WriteString(mutedStyle.Render("a add · e edit · d delete · x disable/enable · s system · h history · u undo · r refresh") + "\n\n")
		if len(p.filter) == 0 {
			b.WriteString(mutedStyle.Render("  (no users)") + "\n")
		}
		// Fit list in remaining space roughly
		for i, ai := range p.filter {
			acct := p.snap.Accounts[ai]
			cur := "  "
			style := itemStyle
			if i == p.idx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			flags := []string{}
			if acct.Locked {
				flags = append(flags, "disabled")
			}
			if acct.IsSystem {
				flags = append(flags, "system")
			}
			flagStr := ""
			if len(flags) > 0 {
				flagStr = " [" + strings.Join(flags, ",") + "]"
			}
			line := fmt.Sprintf("%s%-16s uid=%-5d %s%s", cur, acct.Name, acct.UID, acct.Shell, flagStr)
			b.WriteString(style.Render(line) + "\n")
		}
	}

	return padBlock(strings.Split(b.String(), "\n"), width, height)
}