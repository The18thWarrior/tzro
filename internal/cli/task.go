package cli

import (
	"fmt"
	"os"
	"strings"
	"time"
	"tzro/internal/tui"

	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage active and historical Kahn execution tasks",
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active and past tasks",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		list, err := client.GetTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, list)
			return
		}

		if len(list) == 0 {
			fmt.Println("No tasks found in the execution log.")
			return
		}

		headers := []string{"TASK ID", "CREATED AT", "NODES COUNT", "STATUS PROGRESS"}
		var rows [][]string
		for _, item := range list {
			createdTime := time.Unix(item.CreatedAt, 0).Format("2006-01-02 15:04:05")
			if item.CreatedAt == 0 {
				createdTime = "Active (Memory-Bound)"
			}
			nodesCount := fmt.Sprintf("%d", len(item.Graph.Nodes))

			// Calculate progress summary e.g. "2/3 COMPLETED"
			completed := 0
			running := 0
			failed := 0
			for _, node := range item.Graph.Nodes {
				status := "pending"
				if state, ok := item.States[node.ID]; ok {
					if stateMap, ok := state.(map[string]interface{}); ok {
						if st, ok := stateMap["status"].(string); ok {
							status = st
						}
					} else if stateMap, ok := state.(map[string]string); ok {
						status = stateMap["status"]
					}
				}
				switch status {
				case "completed":
					completed++
				case "running":
					running++
				case "failed":
					failed++
				}
			}
			prog := fmt.Sprintf("%d/%d COMPLETED", completed, len(item.Graph.Nodes))
			if failed > 0 {
				prog += " (1+ FAILED)"
			} else if running > 0 {
				prog += " (RUNNING)"
			}

			rows = append(rows, []string{item.TaskID, createdTime, nodesCount, prog})
		}

		printTable(headers, rows)
	},
}

var taskStatusCmd = &cobra.Command{
	Use:   "status [taskId]",
	Short: "Show detailed nodes, dependencies, and outputs of a specific task graph",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		taskID := args[0]
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		list, err := client.GetTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		var target *tui.TaskStateItem
		for _, item := range list {
			if item.TaskID == taskID {
				target = &item
				break
			}
		}

		if target == nil {
			fmt.Fprintf(os.Stderr, "Error: task not found in SQLite or memory: %s\n", taskID)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, target)
			return
		}

		fmt.Printf("TASK DETAILS: %s\n", target.TaskID)
		createdTime := time.Unix(target.CreatedAt, 0).Format("2006-01-02 15:04:05")
		if target.CreatedAt == 0 {
			createdTime = "Active (Memory-Bound)"
		}
		fmt.Printf("Created: %s | MaxCycles: %d\n\n", createdTime, target.Graph.MaxCycles)

		headers := []string{"NODE ID", "ACTION / TOOL", "STATUS", "OUTPUT PREVIEW"}
		var rows [][]string
		for _, node := range target.Graph.Nodes {
			status := "pending"
			output := ""
			if state, ok := target.States[node.ID]; ok {
				if stateMap, ok := state.(map[string]interface{}); ok {
					if st, ok := stateMap["status"].(string); ok {
						status = st
					}
					if out, ok := stateMap["output"].(string); ok {
						output = out
					}
				} else if stateMap, ok := state.(map[string]string); ok {
					status = stateMap["status"]
					output = stateMap["output"]
				}
			}

			// Clean output formatting
			output = strings.ReplaceAll(output, "\n", " ")
			if len(output) > 40 {
				output = output[:37] + "..."
			}
			if output == "" {
				output = "[No output payload yet]"
			}

			rows = append(rows, []string{node.ID, node.Action, strings.ToUpper(status), output})
		}

		printTable(headers, rows)
	},
}

func init() {
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskStatusCmd)
	RootCmd.AddCommand(taskCmd)
}

// printTable lightweight divider formatting
func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, val := range row {
			if len(val) > widths[i] {
				widths[i] = len(val)
			}
		}
	}

	printDivider(widths)
	fmt.Print("|")
	for i, h := range headers {
		fmt.Printf(" %-*s |", widths[i], h)
	}
	fmt.Println()
	printDivider(widths)

	for _, row := range rows {
		fmt.Print("|")
		for i, val := range row {
			fmt.Printf(" %-*s |", widths[i], val)
		}
		fmt.Println()
	}
	printDivider(widths)
}

func printDivider(widths []int) {
	fmt.Print("+")
	for _, w := range widths {
		fmt.Print(strings.Repeat("-", w+2) + "+")
	}
	fmt.Println()
}
