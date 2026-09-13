package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliZolfaghar/azlinuxadmin/internal/ui"
)

func main() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "error: AZLinuxAdmin must be run as root (use sudo)\n")
		fmt.Fprintf(os.Stderr, "  sudo ./run-dev.sh\n")
		fmt.Fprintf(os.Stderr, "  sudo ./azlinuxadmin\n")
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
