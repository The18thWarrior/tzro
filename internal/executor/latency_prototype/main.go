package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Topology definition
type Topology struct {
	Name   string
	Levels [][]string
}

var Topologies = []Topology{
	{
		Name: "Linear Pipeline (5 sequential steps)",
		Levels: [][]string{
			{"node1"},
			{"node2"},
			{"node3"},
			{"node4"},
			{"node5"},
		},
	},
	{
		Name: "Parallel Fan-Out (1 -> 4 -> 1)",
		Levels: [][]string{
			{"ingest"},
			{"process_a", "process_b", "process_c", "process_d"},
			{"synthesis"},
		},
	},
	{
		Name: "Deep Branching (1 -> 4 -> 4 -> 1)",
		Levels: [][]string{
			{"start"},
			{"fetch_user", "fetch_auth", "fetch_billing", "fetch_history"},
			{"audit_user", "audit_auth", "audit_billing", "audit_history"},
			{"end"},
		},
	},
}

// Model representing application state
type Model struct {
	NodeSleep     time.Duration
	LevelSleep    time.Duration
	ToolWork      time.Duration
	ParallelMode  bool
	TopologyIndex int
	Running       bool
	ProgressLevel int
	NodeStates    map[string]string // "pending" | "running" | "completed"
	StartTime     time.Time
	ElapsedTime   time.Duration
	SimResults    *SimReport
	TickMutex     sync.Mutex
}

type SimReport struct {
	TotalSleepDuration time.Duration
	TotalWorkDuration  time.Duration
	TotalDuration      time.Duration
	Efficiency         float64
}

// Bubble Tea Messages
type tickMsg time.Time
type doneMsg SimReport
type runStepMsg struct {
	levelIndex int
	nodeStates map[string]string
}

func initialModel() *Model {
	m := &Model{
		NodeSleep:     800 * time.Millisecond,
		LevelSleep:    500 * time.Millisecond,
		ToolWork:      200 * time.Millisecond,
		ParallelMode:  true,
		TopologyIndex: 1, // Default to Fan-Out
		NodeStates:    make(map[string]string),
	}
	m.resetNodeStates()
	m.computeInstantReport()
	return m
}

func (m *Model) resetNodeStates() {
	m.NodeStates = make(map[string]string)
	for _, lvl := range Topologies[m.TopologyIndex].Levels {
		for _, nodeID := range lvl {
			m.NodeStates[nodeID] = "pending"
		}
	}
	m.ProgressLevel = -1
}

func (m *Model) computeInstantReport() {
	topo := Topologies[m.TopologyIndex]
	var totalSleep time.Duration
	var totalWork time.Duration
	var totalElapsed time.Duration

	for _, lvl := range topo.Levels {
		var levelSleep time.Duration
		var levelWork time.Duration

		if m.ParallelMode {
			// In parallel mode, work and sleeps happen concurrently, so we take the maximum per level
			levelSleep = m.NodeSleep
			levelWork = m.ToolWork
		} else {
			// In sequential mode, work and sleeps sum up
			levelSleep = m.NodeSleep * time.Duration(len(lvl))
			levelWork = m.ToolWork * time.Duration(len(lvl))
		}

		totalSleep += levelSleep
		totalWork += levelWork
		totalElapsed += levelSleep + levelWork + m.LevelSleep
	}

	efficiency := 0.0
	if totalElapsed > 0 {
		efficiency = float64(totalWork) / float64(totalElapsed) * 100.0
	}

	m.SimResults = &SimReport{
		TotalSleepDuration: totalSleep,
		TotalWorkDuration:  totalWork,
		TotalDuration:      totalElapsed,
		Efficiency:         efficiency,
	}
}

func (m *Model) Init() tea.Cmd {
	return nil
}

// Run simulation tick command
func tickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Live simulation runner
func (m *Model) runSimulation() tea.Cmd {
	return func() tea.Msg {
		topo := Topologies[m.TopologyIndex]
		startTime := time.Now()

		// Copy initial states
		states := make(map[string]string)
		for _, lvl := range topo.Levels {
			for _, nodeID := range lvl {
				states[nodeID] = "pending"
			}
		}

		// Simulate level-by-level
		for _, lvl := range topo.Levels {
			// Set all level nodes to running
			for _, nodeID := range lvl {
				states[nodeID] = "running"
			}

			// Broadcast step start
			// (We scale down the delays in the live GUI demo by 0.5x so the user doesn't have to wait too long)
			scale := 0.5
			nodeDelay := time.Duration(float64(m.NodeSleep+m.ToolWork) * scale)
			levelDelay := time.Duration(float64(m.LevelSleep) * scale)

			if m.ParallelMode {
				time.Sleep(nodeDelay)
			} else {
				for range lvl {
					time.Sleep(nodeDelay)
				}
			}

			// Set all level nodes to completed
			for _, nodeID := range lvl {
				states[nodeID] = "completed"
			}

			time.Sleep(levelDelay)
		}

		// Compute final metrics based on actual settings
		var totalSleep time.Duration
		var totalWork time.Duration
		var totalElapsed time.Duration

		for _, lvl := range topo.Levels {
			var levelSleep time.Duration
			var levelWork time.Duration

			if m.ParallelMode {
				levelSleep = m.NodeSleep
				levelWork = m.ToolWork
			} else {
				levelSleep = m.NodeSleep * time.Duration(len(lvl))
				levelWork = m.ToolWork * time.Duration(len(lvl))
			}

			totalSleep += levelSleep
			totalWork += levelWork
			totalElapsed += levelSleep + levelWork + m.LevelSleep
		}

		efficiency := 0.0
		if totalElapsed > 0 {
			efficiency = float64(totalWork) / float64(totalElapsed) * 100.0
		}

		actualElapsed := time.Since(startTime) // Scaled duration
		_ = actualElapsed                      // We output the theoretical report to be mathematically correct

		return doneMsg{
			TotalSleepDuration: totalSleep,
			TotalWorkDuration:  totalWork,
			TotalDuration:      totalElapsed,
			Efficiency:         efficiency,
		}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.Running {
			// Disable settings changes while simulation is running
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "n": // Toggle Node Sleep
			switch m.NodeSleep {
			case 800 * time.Millisecond:
				m.NodeSleep = 400 * time.Millisecond
			case 400 * time.Millisecond:
				m.NodeSleep = 100 * time.Millisecond
			case 100 * time.Millisecond:
				m.NodeSleep = 0
			default:
				m.NodeSleep = 800 * time.Millisecond
			}
			m.computeInstantReport()

		case "l": // Toggle Level Sleep
			switch m.LevelSleep {
			case 500 * time.Millisecond:
				m.LevelSleep = 200 * time.Millisecond
			case 200 * time.Millisecond:
				m.LevelSleep = 50 * time.Millisecond
			case 50 * time.Millisecond:
				m.LevelSleep = 0
			default:
				m.LevelSleep = 500 * time.Millisecond
			}
			m.computeInstantReport()

		case "w": // Toggle Tool Work
			switch m.ToolWork {
			case 200 * time.Millisecond:
				m.ToolWork = 500 * time.Millisecond
			case 500 * time.Millisecond:
				m.ToolWork = 0
			default:
				m.ToolWork = 200 * time.Millisecond
			}
			m.computeInstantReport()

		case "c": // Toggle Concurrency
			m.ParallelMode = !m.ParallelMode
			m.computeInstantReport()

		case "t": // Toggle Topology
			m.TopologyIndex = (m.TopologyIndex + 1) % len(Topologies)
			m.resetNodeStates()
			m.computeInstantReport()

		case "r": // Run Live Simulation
			m.Running = true
			m.resetNodeStates()
			m.StartTime = time.Now()
			// Spawn the tick updates and runner concurrently
			return m, tea.Batch(tickCmd(), m.runSimulation())
		}

	case tickMsg:
		if m.Running {
			m.ElapsedTime = time.Since(m.StartTime)

			// Drive GUI node animation in a simple mock fashion based on elapsed time
			topo := Topologies[m.TopologyIndex]
			scale := 0.5
			nodeDelay := time.Duration(float64(m.NodeSleep+m.ToolWork) * scale)
			levelDelay := time.Duration(float64(m.LevelSleep) * scale)

			accumulated := time.Duration(0)
			for lvlIdx, lvl := range topo.Levels {
				levelDuration := nodeDelay
				if !m.ParallelMode {
					levelDuration = nodeDelay * time.Duration(len(lvl))
				}

				if m.ElapsedTime >= accumulated && m.ElapsedTime < accumulated+levelDuration {
					m.ProgressLevel = lvlIdx
					for _, nodeID := range lvl {
						m.NodeStates[nodeID] = "running"
					}
				} else if m.ElapsedTime >= accumulated+levelDuration && m.ElapsedTime < accumulated+levelDuration+levelDelay {
					m.ProgressLevel = lvlIdx
					for _, nodeID := range lvl {
						m.NodeStates[nodeID] = "completed"
					}
				} else if m.ElapsedTime >= accumulated+levelDuration+levelDelay {
					for _, nodeID := range lvl {
						m.NodeStates[nodeID] = "completed"
					}
				}
				accumulated += levelDuration + levelDelay
			}

			return m, tickCmd()
		}

	case doneMsg:
		m.Running = false
		report := SimReport(msg)
		m.SimResults = &report
		// Mark all nodes completed
		for k := range m.NodeStates {
			m.NodeStates[k] = "completed"
		}
		return m, nil
	}

	return m, nil
}

func (m *Model) View() string {
	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("99")).
		Padding(1).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("99"))

	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Underline(true)

	boldStyle := lipgloss.NewStyle().Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	var sb strings.Builder

	// Header
	sb.WriteString(titleStyle.Render("tzro Execution Latency Logic Prototype"))
	sb.WriteString("\n\n")

	// State of Question
	sb.WriteString(boldStyle.Render("THE QUESTION BEING PROTOTYPED:"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("How do hardcoded sleeps in executor.go impact overall DAG execution latency across different"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("topologies, and can we safely configure or optimize them away without losing GUI updates?"))
	sb.WriteString("\n\n")

	// Configuration Settings Table
	sb.WriteString(sectionStyle.Render("1. CURRENT RUNTIME CONFIGURATION"))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%s %v (Hardcoded in node execution steps)\n", boldStyle.Render("- Node Sleep:"), m.NodeSleep))
	sb.WriteString(fmt.Sprintf("%s %v (Hardcoded delay between Kahn sorted levels)\n", boldStyle.Render("- Level Sleep:"), m.LevelSleep))
	sb.WriteString(fmt.Sprintf("%s %v (Simulated actual tool completion latency)\n", boldStyle.Render("- Node Work:"), m.ToolWork))
	sb.WriteString(fmt.Sprintf("%s %s (Concurrent level execution)\n", boldStyle.Render("- Concurrency:"), func() string {
		if m.ParallelMode {
			return successStyle.Render("Enabled (Parallel)")
		}
		return warningStyle.Render("Disabled (Sequential)")
	}()))
	sb.WriteString(fmt.Sprintf("%s %s\n\n", boldStyle.Render("- Topology:"), Topologies[m.TopologyIndex].Name))

	// Graph Topology Visualization
	sb.WriteString(sectionStyle.Render("2. GRAPH VISUALIZATION & RUNTIME STATES"))
	sb.WriteString("\n")
	topo := Topologies[m.TopologyIndex]
	for lvlIdx, lvl := range topo.Levels {
		var nodes []string
		for _, nodeID := range lvl {
			status := m.NodeStates[nodeID]
			statusRender := ""
			switch status {
			case "pending":
				statusRender = dimStyle.Render("[PENDING]")
			case "running":
				statusRender = warningStyle.Render("[RUNNING]")
			case "completed":
				statusRender = successStyle.Render("[COMPLETED]")
			}
			nodes = append(nodes, fmt.Sprintf("%s %s", nodeID, statusRender))
		}
		levelArrow := ""
		if lvlIdx > 0 {
			levelArrow = "   │\n   ▼\n"
		}
		sb.WriteString(levelArrow)
		sb.WriteString(fmt.Sprintf("Level %d: { %s }\n", lvlIdx+1, strings.Join(nodes, ", ")))
	}
	sb.WriteString("\n")

	// Live runner status
	if m.Running {
		sb.WriteString(warningStyle.Render(fmt.Sprintf(">>> LIVE SIMULATION RUNNING (Scaled 0.5x for demo): %.1fs elapsed...", m.ElapsedTime.Seconds())))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString(dimStyle.Render("Status: Idle. Press [r] to run live scaled simulation."))
		sb.WriteString("\n\n")
	}

	// Simulation Results
	if m.SimResults != nil {
		sb.WriteString(sectionStyle.Render("3. SIMULATED PROFILE RESULTS (THEORETICAL OUTCOME)"))
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("%s %v\n", boldStyle.Render("Total Sleeptime Overhead:"), m.SimResults.TotalSleepDuration))
		sb.WriteString(fmt.Sprintf("%s %v\n", boldStyle.Render("Total Actual Tool Work:  "), m.SimResults.TotalWorkDuration))

		totalDur := m.SimResults.TotalDuration
		sb.WriteString(fmt.Sprintf("%s %v\n", boldStyle.Render("Total Combined Duration: "), totalDur))

		eff := m.SimResults.Efficiency
		effStr := ""
		if eff > 50.0 {
			effStr = successStyle.Render(fmt.Sprintf("%.1f%%", eff))
		} else if eff > 15.0 {
			effStr = warningStyle.Render(fmt.Sprintf("%.1f%%", eff))
		} else {
			effStr = errorStyle.Render(fmt.Sprintf("%.1f%% (High Sleep Overhead!)", eff))
		}
		sb.WriteString(fmt.Sprintf("%s %s\n\n", boldStyle.Render("Execution Efficiency:    "), effStr))
	}

	// Shortcuts list
	sb.WriteString(dimStyle.Render("=================================================================================="))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%s adjust Node Sleep    %s adjust Level Sleep    %s toggle Concurrency\n", boldStyle.Render("[n]"), boldStyle.Render("[l]"), boldStyle.Render("[c]")))
	sb.WriteString(fmt.Sprintf("%s adjust Node Work     %s change Topology      %s run Live Simulation    %s quit\n", boldStyle.Render("[w]"), boldStyle.Render("[t]"), boldStyle.Render("[r]"), boldStyle.Render("[q]")))

	return sb.String()
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running prototype TUI: %v\n", err)
	}
}
