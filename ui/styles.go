package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Version is printed by --version and shown in the topbar.
const Version = "0.1.0"

const (
	accentAWS      = "#ff9900"
	accentGCP      = "#4285f4"
	accentFallback = "#8be9fd"
)

func accentFor(providerID string) lipgloss.Color {
	switch providerID {
	case "aws":
		return lipgloss.Color(accentAWS)
	case "gcp":
		return lipgloss.Color(accentGCP)
	default:
		return lipgloss.Color(accentFallback)
	}
}

var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	okStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	filterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sortStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
)
