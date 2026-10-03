package context

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// SanitizeTerminalText removes terminal control characters and escape codes from text.
func SanitizeTerminalText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
		} else if r == '\t' {
			b.WriteString("  ")
		} else if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9F) {
			// Strip control characters and escape sequences
			continue
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TreeRenderer renders ImpactReport as a static ANSI tree.
type TreeRenderer struct {
	TermWidth int
	NoColor   bool
	renderer  *lipgloss.Renderer
}

// NewTreeRenderer creates a TreeRenderer.
func NewTreeRenderer(termWidth int, noColor bool) *TreeRenderer {
	if termWidth <= 0 {
		termWidth = 80
	}
	return &TreeRenderer{
		TermWidth: termWidth,
		NoColor:   noColor,
	}
}

// Render formats the ImpactReport as an ANSI terminal tree.
func (tr *TreeRenderer) Render(report *ImpactReport) string {
	if report == nil {
		return ""
	}

	if tr.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	} else {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	var sb strings.Builder

	// Style definitions
	var (
		headerStyle = lipgloss.NewStyle().Bold(true)
		symStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4"))
		callerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFFF"))
		testStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
		configStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))
		mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
		warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555"))
	)

	if tr.NoColor {
		symStyle = lipgloss.NewStyle().Bold(true)
		callerStyle = lipgloss.NewStyle()
		testStyle = lipgloss.NewStyle()
		configStyle = lipgloss.NewStyle()
		mutedStyle = lipgloss.NewStyle()
		warnStyle = lipgloss.NewStyle()
	}

	// 1. Check empty states
	if report.Coverage != nil && report.Coverage.NoChanges {
		return "No changes detected in requested scope.\n"
	}

	divider := strings.Repeat("─", min(tr.TermWidth, 72))

	// 2. Summary header
	sb.WriteString(divider + "\n")
	sb.WriteString(headerStyle.Render("Blast Radius Analysis") + "\n")
	sb.WriteString(fmt.Sprintf("Changed symbols:        %d\n", len(report.ChangedSymbols)))
	sb.WriteString(fmt.Sprintf("Potential blast radius: %d unique reference site(s) (%d total edges)\n",
		report.UniqueReferencesCount, report.TotalEdgesCount))
	sb.WriteString(fmt.Sprintf("Affected modules:       %d\n", len(report.AffectedModules)))
	sb.WriteString(fmt.Sprintf("Candidate test files:   %d\n", len(report.CandidateTestFiles)))
	sb.WriteString(divider + "\n")

	// Incomplete discovery or fallback warnings
	if report.Coverage != nil {
		if report.Coverage.IncompleteDiscovery {
			reasons := append([]string{}, report.Coverage.UnsupportedSyntax...)
			reasons = append(reasons, report.Coverage.UnresolvedImports...)
			reasons = append(reasons, report.Coverage.AdapterErrors...)
			reasonStr := "incomplete discovery"
			if len(reasons) > 0 {
				reasonStr = strings.Join(reasons, "; ")
			}
			sb.WriteString(warnStyle.Render(fmt.Sprintf("Notice: %s", SanitizeTerminalText(reasonStr))) + "\n")
		}
		if report.Coverage.FallbackUsed && report.Coverage.FallbackReason != "" {
			sb.WriteString(mutedStyle.Render(fmt.Sprintf("Fallback adapter: %s", SanitizeTerminalText(report.Coverage.FallbackReason))) + "\n")
		}
	}

	if len(report.ChangedSymbols) == 0 {
		sb.WriteString("No changed symbols discovered in diff scope.\n")
		return sb.String()
	}

	if report.TotalEdgesCount == 0 && (report.Coverage == nil || !report.Coverage.NoReferences) {
		if report.Coverage != nil && report.Coverage.NoReferences {
			sb.WriteString("No references discovered for changed symbols.\n")
			return sb.String()
		}
	}

	// 3. Group references by source symbol
	edgesBySymbol := make(map[string][]RawReference)
	for _, ref := range report.ReferenceEdges {
		key := ref.SymbolName
		if ref.SourceSymbol != nil && ref.SourceSymbol.Name != "" {
			key = ref.SourceSymbol.Name
		}
		edgesBySymbol[key] = append(edgesBySymbol[key], ref)
	}

	// Sort changed symbols stably
	sortedSymbols := append([]Symbol{}, report.ChangedSymbols...)
	sort.Slice(sortedSymbols, func(i, j int) bool {
		if sortedSymbols[i].FilePath != sortedSymbols[j].FilePath {
			return sortedSymbols[i].FilePath < sortedSymbols[j].FilePath
		}
		if sortedSymbols[i].StartLine != sortedSymbols[j].StartLine {
			return sortedSymbols[i].StartLine < sortedSymbols[j].StartLine
		}
		return sortedSymbols[i].Name < sortedSymbols[j].Name
	})

	// 4. Render each symbol tree
	for sIdx, sym := range sortedSymbols {
		cleanSymName := SanitizeTerminalText(sym.Name)
		cleanPath := SanitizeTerminalText(sym.FilePath)
		cleanKind := SanitizeTerminalText(sym.Kind)
		if cleanKind == "" {
			cleanKind = "symbol"
		}

		symHeader := fmt.Sprintf("● [%s] %s (%s:%d)", cleanKind, cleanSymName, cleanPath, sym.StartLine)
		sb.WriteString("\n" + symStyle.Render(symHeader) + "\n")

		edges := edgesBySymbol[sym.Name]
		if len(edges) == 0 {
			sb.WriteString("  └── " + mutedStyle.Render("(no downstream references discovered)") + "\n")
			continue
		}

		// Sort edges stably: Relationship, FilePath, StartLine
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].Relationship != edges[j].Relationship {
				return edges[i].Relationship < edges[j].Relationship
			}
			if edges[i].FilePath != edges[j].FilePath {
				return edges[i].FilePath < edges[j].FilePath
			}
			return edges[i].StartLine < edges[j].StartLine
		})

		// Cap display items per symbol to avoid overwhelming the terminal
		maxDisplay := 50
		displayEdges := edges
		truncatedCount := 0
		if len(edges) > maxDisplay {
			displayEdges = edges[:maxDisplay]
			truncatedCount = len(edges) - maxDisplay
		}

		for eIdx, edge := range displayEdges {
			isLast := (eIdx == len(displayEdges)-1) && truncatedCount == 0
			branch := "├── "
			if isLast {
				branch = "└── "
			}

			rel := edge.Relationship
			prec := edge.Precision
			if prec == "" {
				prec = PrecisionSyntactic
			}
			cleanRefPath := SanitizeTerminalText(edge.FilePath)

			var styledRel string
			switch rel {
			case RelCaller:
				styledRel = callerStyle.Render("[" + rel + "]")
			case RelTest:
				styledRel = testStyle.Render("[" + rel + "]")
			case RelConfig:
				styledRel = configStyle.Render("[" + rel + "]")
			default:
				styledRel = mutedStyle.Render("[" + rel + "]")
			}

			precLabel := mutedStyle.Render("[" + prec + "]")
			lineInfo := fmt.Sprintf("%s:%d", cleanRefPath, edge.StartLine)

			line := fmt.Sprintf("  %s%s %s %s", branch, styledRel, precLabel, lineInfo)
			// Truncate line if it exceeds terminal width
			if tr.TermWidth > 10 && lipgloss.Width(line) > tr.TermWidth {
				line = line[:tr.TermWidth-3] + "..."
			}

			sb.WriteString(line + "\n")
		}

		if truncatedCount > 0 {
			sb.WriteString(fmt.Sprintf("  └── %s\n", mutedStyle.Render(fmt.Sprintf("... and %d more reference(s)", truncatedCount))))
		}

		_ = sIdx
	}

	return sb.String()
}

// RenderImpactTree renders an ImpactReport with terminal width and color control.
func RenderImpactTree(report *ImpactReport, termWidth int, noColor bool) string {
	renderer := NewTreeRenderer(termWidth, noColor)
	return renderer.Render(report)
}
