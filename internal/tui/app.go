package tui

import (
	"context"
	"fmt"
	"strings"
	"tzro/internal/compiler"
	"tzro/internal/stream"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Panel int

const (
	DashboardPanel Panel = iota
	LogsPanel
	TaskDAGPanel
	MemoriesPanel
	MCPPanel
)

// TasksHydratedMsg is sent to Bubble Tea program when REST tasks hydration completes.
type TasksHydratedMsg []TaskStateItem

// Model holds the entire TUI application state.
type Model struct {
	ActivePanel  Panel
	Client       TZROClient
	ConsoleLog   []string
	SidebarIndex int
	Quitting     bool
	Width        int
	Height       int
	StreamChan   chan stream.StreamChunk
	StreamCancel context.CancelFunc
	ActiveGraph  *compiler.ExecutionGraph
	ActiveLevels [][]string
	TreeLayout   bool
	IsHydrating  bool
}

func NewModel(client TZROClient) Model {
	return Model{
		ActivePanel:  DashboardPanel,
		Client:       client,
		ConsoleLog:   make([]string, 0),
		SidebarIndex: 0,
		Quitting:     false,
		TreeLayout:   false,
		IsHydrating:  false,
	}
}

func (m Model) Init() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.Client.GetEventsStream(ctx)
	if err == nil {
		m.StreamCancel = cancel
		m.StreamChan = make(chan stream.StreamChunk, 100)

		// Spawn dynamic forwarder loop
		go func() {
			defer close(m.StreamChan)
			for {
				select {
				case <-ctx.Done():
					return
				case chunk, ok := <-ch:
					if !ok {
						return
					}
					select {
					case m.StreamChan <- chunk:
					case <-ctx.Done():
						return
					}
				}
			}
		}()

		return listenOnChannel(m.StreamChan)
	}
	cancel() // clean up immediately if offline
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TasksHydratedMsg:
		m.IsHydrating = false
		if len(msg) > 0 {
			// Hydrate TUI with the most recent running or past task
			latest := msg[len(msg)-1]
			m.ActiveGraph = latest.Graph

			// Reconstruct standard levels from edges topologically using Kahn sort compiler
			levels, err := compiler.CompileAndSort(latest.Graph)
			if err == nil {
				m.ActiveLevels = levels
			} else {
				// Fallback flat layering if graph contains cyclic loops
				var lvl []string
				for _, node := range latest.Graph.Nodes {
					lvl = append(lvl, node.ID)
				}
				m.ActiveLevels = [][]string{lvl}
			}
		}
		return m, nil

	case StreamMsg:
		// 1. Process telemetry logs
		logLine := msg.Content
		if msg.Source != "" {
			logLine = "[" + strings.ToUpper(msg.Source) + "] " + logLine
		}
		m.ConsoleLog = append(m.ConsoleLog, logLine)
		if len(m.ConsoleLog) > 500 {
			m.ConsoleLog = m.ConsoleLog[len(m.ConsoleLog)-500:]
		}

		// 2. Animate Graph Node states dynamically if SSE matches our active Graph
		if m.ActiveGraph != nil && msg.TaskID == m.ActiveGraph.TaskID {
			// Find and update node status
			for idx, node := range m.ActiveGraph.Nodes {
				// Event content matches node ID transitions e.g. "node_id: completed"
				if strings.Contains(msg.Content, node.ID) {
					if strings.Contains(msg.Content, "completed") || strings.Contains(msg.Content, "success") {
						m.ActiveGraph.Nodes[idx].Status = "completed"
					} else if strings.Contains(msg.Content, "running") {
						m.ActiveGraph.Nodes[idx].Status = "running"
					} else if strings.Contains(msg.Content, "failed") {
						m.ActiveGraph.Nodes[idx].Status = "failed"
					}
				}
			}
		}

		// Continue recursively listening to SSE StreamBus
		if m.StreamChan != nil {
			return m, listenOnChannel(m.StreamChan)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.Quitting = true
			if m.StreamCancel != nil {
				m.StreamCancel()
			}
			return m, tea.Quit

		case "up", "k":
			if m.SidebarIndex > 0 {
				m.SidebarIndex--
			}
			return m, nil

		case "down", "j":
			if m.SidebarIndex < 4 { // Cap at 4 (5 menu items)
				m.SidebarIndex++
			}
			return m, nil

		case "enter":
			m.ActivePanel = Panel(m.SidebarIndex)
			if m.ActivePanel == TaskDAGPanel {
				m.IsHydrating = true
				return m, func() tea.Msg {
					list, err := m.Client.GetTasks()
					if err != nil {
						return TasksHydratedMsg(nil)
					}
					return TasksHydratedMsg(list)
				}
			}
			return m, nil

		case "tab", "v":
			if m.ActivePanel == TaskDAGPanel {
				m.TreeLayout = !m.TreeLayout
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}

	return m, nil
}

func (m Model) View() string {
	if m.Quitting {
		return "Exited tzro TUI Dashboard.\n"
	}

	// 1. Sidebar rendering
	menuItems := []string{
		"Dashboard  ",
		"Stream Logs",
		"Task DAG   ",
		"Memories   ",
		"MCP Hosts  ",
	}

	sidebarStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("240")).
		PaddingRight(2).
		MarginRight(2)

	activeItemStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("99")) // HSL Violet purple select

	var sb strings.Builder
	sb.WriteString("  tzro Dashboard\n")
	sb.WriteString("================\n\n")

	for i, item := range menuItems {
		pointer := "  "
		if i == m.SidebarIndex {
			pointer = "> "
		}

		line := pointer + item
		if i == m.SidebarIndex {
			sb.WriteString(activeItemStyle.Render(line) + "\n")
		} else {
			sb.WriteString(line + "\n")
		}
	}

	sidebarView := sidebarStyle.Render(sb.String())

	// 2. Main content rendering based on active panel selection
	contentStyle := lipgloss.NewStyle().
		Width(m.Width - 25).
		Height(m.Height - 4)

	var mainView string
	switch m.ActivePanel {
	case DashboardPanel:
		mainView = "=== DAEMON & SIDECAR CONSOLE ===\n\nSidecar status is pre-warmed.\nMonitor metrics and RAM locks live.\n\nConnected Mode details:\nUrl: http://localhost:8080"
	case LogsPanel:
		logContent := strings.Join(m.ConsoleLog, "\n")
		if logContent == "" {
			logContent = "No events recorded yet on StreamBus..."
		}
		mainView = "=== SSE STREAMBUS LOGS ===\n\nTailing /api/events live telemetry:\n\n" + logContent
	case TaskDAGPanel:
		if m.IsHydrating {
			mainView = "=== KAHN DAG TASK GRAPH ===\n\nHydrating task list checkpoints from database..."
		} else if m.ActiveGraph == nil {
			mainView = "=== KAHN DAG TASK GRAPH ===\n\nNo tasks found. Submit a task using 'tzro chat' to visualize progress."
		} else {
			var graphView string
			if m.TreeLayout {
				graphView = renderTreeLayout(m.ActiveGraph, m.ActiveLevels)
			} else {
				graphView = renderColumnLayout(m.ActiveGraph, m.ActiveLevels)
			}
			mainView = fmt.Sprintf("=== KAHN DAG TASK GRAPH (ID: %s) ===\n\n%s\n\n[Tab / v] Toggle Layout | Active view: %s",
				m.ActiveGraph.TaskID,
				graphView,
				func() string {
					if m.TreeLayout {
						return "TREE HIERARCHY"
					}
					return "COLUMN DAG"
				}())
		}
	case MemoriesPanel:
		mainView = "=== PERSISTENT KV FACT MEMORIES ===\n\nDirect SQL read-only fact logs list.\nUse 'tzro memory list' to query memories in tabular formats."
	case MCPPanel:
		mainView = "=== MCP STDIO HOOD DAEMONS ===\n\n16 sandbox integration server registries.\nUse 'tzro mcp list' to discover dynamic tools."
	}

	contentView := contentStyle.Render(mainView)

	// Combine sidebar and main panel horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarView, contentView)
}
