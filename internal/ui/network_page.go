package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AliZolfaghar/azlinuxadmin/internal/change"
	"github.com/AliZolfaghar/azlinuxadmin/internal/network"
)

type networkSection int

const (
	netSecHostname networkSection = iota
	netSecIface
	netSecGateway
	netSecDNS
	netSecHistory
)

type networkMode int

const (
	netModeMenu networkMode = iota
	netModeEditHostname
	netModePickIface
	netModeEditIface
	netModeEditGateway
	netModeEditDNS
	netModeConfirm
	netModeHistory
)

type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmHostname
	confirmIface
	confirmGateway
	confirmDNS
	confirmUndo
)

type networkPage struct {
	journal *change.Journal
	snap    network.Snapshot
	history []change.Entry

	section networkSection
	mode    networkMode

	ifaceIdx   int
	historyIdx int
	fieldIdx   int // 0=address, 1=gateway (static mode)
	dhcp4      bool
	addrDraft  string
	gwDraft    string

	input   textinput.Model
	status  string
	errMsg  string
	confirm confirmAction
	undoID  string
	loaded  bool
}

func newNetworkPage() networkPage {
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 42
	ti.Prompt = "› "
	j, _ := change.Open()
	return networkPage{
		journal: j,
		input:   ti,
	}
}

type netRefreshMsg struct {
	snap    network.Snapshot
	history []change.Entry
	err     error
}

func (n networkPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		snap := network.LoadSnapshot()
		var hist []change.Entry
		var err error
		if n.journal != nil {
			hist, err = n.journal.List()
		}
		var filtered []change.Entry
		for _, e := range hist {
			if strings.HasPrefix(e.Kind, "network.") {
				filtered = append(filtered, e)
			}
		}
		return netRefreshMsg{snap: snap, history: filtered, err: err}
	}
}

func (n networkPage) Init() tea.Cmd {
	return n.refreshCmd()
}

func (n *networkPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case netRefreshMsg:
		n.loaded = true
		n.snap = msg.snap
		n.history = msg.history
		if msg.err != nil {
			n.errMsg = msg.err.Error()
		}
		if n.ifaceIdx >= len(n.snap.Interfaces) && len(n.snap.Interfaces) > 0 {
			n.ifaceIdx = 0
		}
		return nil
	}

	switch n.mode {
	case netModeEditHostname, netModeEditDNS, netModeEditGateway, netModeEditIface:
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc":
				n.input.Blur()
				if n.mode == netModeEditIface {
					n.mode = netModePickIface
				} else {
					n.mode = netModeMenu
				}
				return nil
			case "d":
				if n.mode == netModeEditIface {
					n.persistIfaceField()
					n.dhcp4 = !n.dhcp4
					n.fieldIdx = 0
					n.loadIfaceField()
					return nil
				}
			case "tab", "shift+tab":
				if n.mode == netModeEditIface && !n.dhcp4 {
					n.persistIfaceField()
					n.fieldIdx = (n.fieldIdx + 1) % 2
					n.loadIfaceField()
					return nil
				}
			case "enter":
				return n.handleEditEnter()
			}
		}
		var cmd tea.Cmd
		n.input, cmd = n.input.Update(msg)
		return cmd
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if n.mode == netModeConfirm {
		switch km.String() {
		case "y", "Y", "enter":
			return n.runConfirmed()
		case "n", "N", "esc":
			n.confirm = confirmNone
			n.mode = netModeMenu
			n.status = "cancelled"
		}
		return nil
	}

	switch n.mode {
	case netModePickIface:
		switch km.String() {
		case "esc":
			n.mode = netModeMenu
		case "up", "k":
			if n.ifaceIdx > 0 {
				n.ifaceIdx--
			}
		case "down", "j":
			if n.ifaceIdx < len(n.snap.Interfaces)-1 {
				n.ifaceIdx++
			}
		case "enter":
			n.beginEditIface()
		}
		return nil

	case netModeHistory:
		switch km.String() {
		case "esc":
			n.mode = netModeMenu
		case "up", "k":
			if n.historyIdx > 0 {
				n.historyIdx--
			}
		case "down", "j":
			if n.historyIdx < len(n.history)-1 {
				n.historyIdx++
			}
		case "u", "enter":
			if len(n.history) == 0 {
				return nil
			}
			e := n.history[n.historyIdx]
			if e.Undone {
				n.errMsg = "already undone"
				return nil
			}
			n.undoID = e.ID
			n.confirm = confirmUndo
			n.mode = netModeConfirm
			n.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
		}
		return nil
	}

	switch km.String() {
	case "up", "k":
		if n.section > 0 {
			n.section--
		}
	case "down", "j":
		if n.section < netSecHistory {
			n.section++
		}
	case "enter":
		return n.openSection()
	case "r":
		n.status = "refreshing…"
		return n.refreshCmd()
	case "u":
		if n.journal == nil {
			n.errMsg = "journal unavailable"
			return nil
		}
		e, err := n.journal.LastActive("network.")
		if err != nil || e == nil {
			n.errMsg = "no undoable network change"
			return nil
		}
		n.undoID = e.ID
		n.confirm = confirmUndo
		n.mode = netModeConfirm
		n.status = fmt.Sprintf("Undo %s — y confirm / n cancel", e.Summary)
	}
	return nil
}

func (n *networkPage) openSection() tea.Cmd {
	n.errMsg = ""
	switch n.section {
	case netSecHostname:
		n.mode = netModeEditHostname
		n.input.SetValue(n.snap.Hostname)
		n.input.CursorEnd()
		n.input.Focus()
		n.status = "edit hostname — Enter apply, Esc cancel"
	case netSecIface:
		if len(n.snap.Interfaces) == 0 {
			n.errMsg = "no interfaces found"
			return nil
		}
		n.mode = netModePickIface
		n.status = "select NIC — Enter edit, Esc back"
	case netSecGateway:
		n.mode = netModeEditGateway
		n.input.SetValue(n.snap.Gateway)
		n.input.CursorEnd()
		n.input.Focus()
		n.status = "edit default gateway — Enter apply, Esc cancel"
	case netSecDNS:
		n.mode = netModeEditDNS
		n.input.SetValue(strings.Join(n.snap.DNS, ", "))
		n.input.CursorEnd()
		n.input.Focus()
		n.status = "edit DNS (comma-separated) — Enter apply, Esc cancel"
	case netSecHistory:
		n.mode = netModeHistory
		n.historyIdx = 0
		n.status = "history — u/Enter undo selected, Esc back"
	}
	return nil
}

func (n *networkPage) beginEditIface() {
	ifc := n.snap.Interfaces[n.ifaceIdx]
	n.mode = netModeEditIface
	n.fieldIdx = 0
	n.dhcp4 = ifc.DHCP4 != nil && *ifc.DHCP4
	n.addrDraft = ""
	if len(ifc.Addresses) > 0 {
		n.addrDraft = ifc.Addresses[0]
	} else if len(ifc.IPv4) > 0 {
		n.addrDraft = ifc.IPv4[0]
	}
	n.gwDraft = ifc.Gateway
	n.loadIfaceField()
	n.status = fmt.Sprintf("edit %s — d DHCP toggle, Tab fields, Enter apply, Esc back", ifc.Name)
}

func (n *networkPage) persistIfaceField() {
	val := strings.TrimSpace(n.input.Value())
	if n.fieldIdx == 0 {
		n.addrDraft = val
	} else {
		n.gwDraft = val
	}
}

func (n *networkPage) loadIfaceField() {
	if n.dhcp4 {
		n.input.Blur()
		n.input.SetValue("")
		return
	}
	if n.fieldIdx == 0 {
		n.input.SetValue(n.addrDraft)
	} else {
		n.input.SetValue(n.gwDraft)
	}
	n.input.CursorEnd()
	n.input.Focus()
}

func (n *networkPage) handleEditEnter() tea.Cmd {
	switch n.mode {
	case netModeEditHostname:
		n.input.Blur()
		n.confirm = confirmHostname
		n.mode = netModeConfirm
		n.status = fmt.Sprintf("Set hostname to %q — y confirm / n cancel", strings.TrimSpace(n.input.Value()))
	case netModeEditGateway:
		n.input.Blur()
		n.confirm = confirmGateway
		n.mode = netModeConfirm
		n.status = fmt.Sprintf("Set default gateway to %q (may drop SSH if wrong) — y confirm / n cancel", strings.TrimSpace(n.input.Value()))
	case netModeEditDNS:
		n.input.Blur()
		n.confirm = confirmDNS
		n.mode = netModeConfirm
		n.status = fmt.Sprintf("Set DNS to %q — y confirm / n cancel", strings.TrimSpace(n.input.Value()))
	case netModeEditIface:
		n.persistIfaceField()
		n.input.Blur()
		n.confirm = confirmIface
		n.mode = netModeConfirm
		ifc := n.snap.Interfaces[n.ifaceIdx]
		n.status = fmt.Sprintf("Apply config for %s (may drop SSH if wrong) — y confirm / n cancel", ifc.Name)
	}
	return nil
}

func (n *networkPage) runConfirmed() tea.Cmd {
	action := n.confirm
	if n.journal == nil {
		n.confirm = confirmNone
		n.mode = netModeMenu
		n.errMsg = "journal unavailable"
		return nil
	}

	var err error
	switch action {
	case confirmHostname:
		_, err = network.SetHostname(n.journal, strings.TrimSpace(n.input.Value()))
	case confirmDNS:
		parts := splitCSV(n.input.Value())
		iface := ""
		if len(n.snap.Interfaces) > 0 {
			iface = n.snap.Interfaces[0].Name
		}
		_, err = network.ApplyDNS(n.journal, iface, parts)
	case confirmGateway:
		_, err = network.ApplyGateway(n.journal, n.snap.GatewayIface, strings.TrimSpace(n.input.Value()))
	case confirmIface:
		ifc := n.snap.Interfaces[n.ifaceIdx]
		upd := network.IfaceUpdate{
			Name:        ifc.Name,
			DHCP4:       n.dhcp4,
			AddressCIDR: n.addrDraft,
			Gateway:     n.gwDraft,
			Nameservers: ifc.Nameservers,
		}
		if len(upd.Nameservers) == 0 {
			upd.Nameservers = n.snap.DNS
		}
		_, err = network.ApplyIfaceConfig(n.journal, upd)
	case confirmUndo:
		_, err = network.UndoNetworkChange(n.journal, n.undoID)
	default:
		n.confirm = confirmNone
		n.mode = netModeMenu
		return nil
	}

	n.confirm = confirmNone
	n.mode = netModeMenu
	if err != nil {
		n.errMsg = err.Error()
		return nil
	}
	switch action {
	case confirmHostname:
		n.status = "hostname updated (undo with u)"
	case confirmDNS:
		n.status = "DNS updated (undo with u)"
	case confirmGateway:
		n.status = "gateway updated (undo with u)"
	case confirmIface:
		n.status = "interface updated (undo with u)"
	case confirmUndo:
		n.status = "change undone"
		n.undoID = ""
	}
	return n.refreshCmd()
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (n networkPage) View(width, height int) string {
	var b strings.Builder

	// First row: detected network manager used for applies.
	mgrLabel := "detecting…"
	if n.loaded {
		mgrLabel = n.snap.Detection.Label
		if mgrLabel == "" {
			mgrLabel = string(n.snap.Detection.Manager)
		}
	}
	b.WriteString(activeItemStyle.Render("Network manager: ") + keyStyle.Render(mgrLabel) + "\n")

	if !n.loaded {
		b.WriteString(mutedStyle.Render("loading…") + "\n")
		return padBlock(strings.Split(b.String(), "\n"), width, height)
	}
	if os.Geteuid() != 0 {
		b.WriteString(errorStyle.Render("must run as root (sudo)") + "\n")
	}
	if !n.snap.Detection.Writable {
		b.WriteString(errorStyle.Render("cannot apply via "+mgrLabel+": "+n.snap.Detection.Reason) + "\n")
	}
	if n.snap.Warning != "" && n.snap.Warning != n.snap.Detection.Reason {
		b.WriteString(mutedStyle.Render("⚠ "+n.snap.Warning) + "\n")
	}
	if n.errMsg != "" {
		b.WriteString(errorStyle.Render("error: "+n.errMsg) + "\n")
	}
	if n.status != "" {
		b.WriteString(mutedStyle.Render(n.status) + "\n")
	}
	b.WriteString("\n")

	switch n.mode {
	case netModeEditHostname:
		b.WriteString("Hostname:\n")
		b.WriteString(n.input.View() + "\n")
	case netModeEditGateway:
		b.WriteString("Default gateway:\n")
		b.WriteString(n.input.View() + "\n")
		if n.snap.GatewayIface != "" {
			b.WriteString(mutedStyle.Render("  via interface: "+n.snap.GatewayIface) + "\n")
		}
	case netModeEditDNS:
		b.WriteString("DNS servers (comma-separated):\n")
		b.WriteString(n.input.View() + "\n")
	case netModePickIface:
		b.WriteString("Interfaces:\n")
		for i, ifc := range n.snap.Interfaces {
			cur := "  "
			style := itemStyle
			if i == n.ifaceIdx {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			addr := strings.Join(ifc.IPv4, ",")
			if addr == "" {
				addr = "-"
			}
			line := fmt.Sprintf("%s%s  %s  %s", cur, ifc.Name, ifc.State, addr)
			b.WriteString(style.Render(line) + "\n")
		}
	case netModeEditIface:
		ifc := n.snap.Interfaces[n.ifaceIdx]
		mode := "static"
		if n.dhcp4 {
			mode = "dhcp"
		}
		b.WriteString(fmt.Sprintf("Configure %s\n", ifc.Name))
		b.WriteString(fmt.Sprintf("  Mode: %s\n", mode))
		if n.dhcp4 {
			b.WriteString(mutedStyle.Render("  Address/gateway from DHCP") + "\n")
		} else {
			addrLabel := "  Address CIDR:"
			gwLabel := "  Gateway:"
			if n.fieldIdx == 0 {
				addrLabel = activeItemStyle.Render("▸ Address CIDR:")
				b.WriteString(addrLabel + "\n")
				b.WriteString(n.input.View() + "\n")
				b.WriteString(gwLabel + " " + mutedStyle.Render(n.gwDraft) + "\n")
			} else {
				b.WriteString(addrLabel + " " + mutedStyle.Render(n.addrDraft) + "\n")
				b.WriteString(activeItemStyle.Render("▸ Gateway:") + "\n")
				b.WriteString(n.input.View() + "\n")
			}
		}
		b.WriteString(mutedStyle.Render("  DNS: "+strings.Join(n.snap.DNS, ", ")+" (edit via DNS section)") + "\n")
	case netModeHistory:
		b.WriteString("Network change history:\n")
		if len(n.history) == 0 {
			b.WriteString(mutedStyle.Render("  (empty)") + "\n")
		}
		for i, e := range n.history {
			cur := "  "
			style := itemStyle
			if i == n.historyIdx {
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
	case netModeConfirm:
		b.WriteString(activeItemStyle.Render("Confirm") + "\n\n")
		b.WriteString(n.status + "\n")
	default:
		gwVal := n.snap.Gateway
		if gwVal != "" && n.snap.GatewayIface != "" {
			gwVal = fmt.Sprintf("%s (%s)", n.snap.Gateway, n.snap.GatewayIface)
		}
		rows := []struct {
			label string
			value string
		}{
			{"Hostname", n.snap.Hostname},
			{"Interfaces", ifaceSummary(n.snap)},
			{"Gateway", gwVal},
			{"DNS", strings.Join(n.snap.DNS, ", ")},
			{"History", fmt.Sprintf("%d changes", countActive(n.history))},
		}
		for i, row := range rows {
			cur := "  "
			style := itemStyle
			if networkSection(i) == n.section {
				cur = cursorStyle.Render("▸") + " "
				style = activeItemStyle
			}
			val := row.value
			if val == "" {
				val = "-"
			}
			b.WriteString(style.Render(cur+row.label) + "  " + mutedStyle.Render(val) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Enter open · r refresh · u undo last · Esc sidebar") + "\n")
	}

	return padBlock(strings.Split(b.String(), "\n"), width, height)
}

func ifaceSummary(s network.Snapshot) string {
	if len(s.Interfaces) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(s.Interfaces))
	for _, ifc := range s.Interfaces {
		a := "-"
		if len(ifc.IPv4) > 0 {
			a = ifc.IPv4[0]
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", ifc.Name, a))
	}
	return strings.Join(parts, ", ")
}

func countActive(hist []change.Entry) int {
	n := 0
	for _, e := range hist {
		if !e.Undone {
			n++
		}
	}
	return n
}

var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
