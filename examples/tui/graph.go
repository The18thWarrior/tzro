package tui

import (
	"fmt"
	"strings"
	"tzro/internal/compiler"

	"github.com/charmbracelet/lipgloss"
)

// renderColumnLayout draws Kahn levels side-by-side joined horizontally by arrows.
func renderColumnLayout(g *compiler.ExecutionGraph, levels [][]string) string {
	if g == nil || len(levels) == 0 {
		return "No active execution graph compiled yet."
	}

	var cols []string

	// Style templates
	successStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("46")). // Green
		Padding(0, 1)

	runningStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")). // Orange/Yellow
		Padding(0, 1)

	pendingStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("244")). // Slate Grey
		Padding(0, 1)

	for i, lvl := range levels {
		var lvlNodes []string
		for _, nodeID := range lvl {
			// Find node details
			var target compiler.GraphNode
			found := false
			for _, n := range g.Nodes {
				if n.ID == nodeID {
					target = n
					found = true
					break
				}
			}

			if !found {
				continue
			}

			// Format status message
			statusLabel := "[⌛ PENDING]"
			boxStyle := pendingStyle
			switch target.Status {
			case "completed":
				statusLabel = "[✔ SUCCESS]"
				boxStyle = successStyle
			case "running":
				statusLabel = "[▶ RUNNING]"
				boxStyle = runningStyle
			case "failed":
				statusLabel = "[❌ FAILED]"
				boxStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("196")).Padding(0, 1)
			}

			nodeBoxContent := fmt.Sprintf("%s\n%s\n%s", target.ID, target.Action, statusLabel)
			lvlNodes = append(lvlNodes, boxStyle.Render(nodeBoxContent))
		}

		// Join nodes in the same level vertically (in case of parallel nodes)
		levelView := lipgloss.JoinVertical(lipgloss.Center, lvlNodes...)
		cols = append(cols, levelView)

		// Append horizontal transition arrow between levels (except the last one)
		if i < len(levels)-1 {
			arrowStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("242")).
				Margin(2, 1)
			cols = append(cols, arrowStyle.Render("──►"))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, cols...)
}

// renderTreeLayout draws Kahn levels top-to-bottom like an indented directory branch tree.
func renderTreeLayout(g *compiler.ExecutionGraph, levels [][]string) string {
	if g == nil || len(levels) == 0 {
		return "No active execution graph compiled yet."
	}

	var sb strings.Builder
	sb.WriteString(" Kahn DAG Levels (Indented Tree Layout):\n\n")

	for i, lvl := range levels {
		indent := ""
		prefix := "● "
		if i > 0 {
			indent = strings.Repeat("  ", i)
			if i == len(levels)-1 {
				prefix = "└── ↳ "
			} else {
				prefix = "├── ↳ "
			}
		}

		for _, nodeID := range lvl {
			// Find node details
			var target compiler.GraphNode
			found := false
			for _, n := range g.Nodes {
				if n.ID == nodeID {
					target = n
					found = true
					break
				}
			}

			if !found {
				continue
			}

			statusText := "PENDING"
			statusColor := "244" // Grey
			switch target.Status {
			case "completed":
				statusText = "SUCCESS"
				statusColor = "46" // Green
			case "running":
				statusText = "RUNNING"
				statusColor = "214" // Yellow
			case "failed":
				statusText = "FAILED"
				statusColor = "196" // Red
			}

			styledStatus := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Bold(true).Render(statusText)
			sb.WriteString(fmt.Sprintf("%s%s[%s] Tool: %s (Status: %s)\n", indent, prefix, target.ID, target.Action, styledStatus))
		}
	}

	return sb.String()
}
