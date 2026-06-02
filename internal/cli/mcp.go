package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Inspect active Model Context Protocol (MCP) stdio hosts",
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active stdio MCP daemons registered with the host",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		daemons, err := client.GetMCPList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, daemons)
			return
		}

		if len(daemons) == 0 {
			fmt.Println("No active MCP stdio host daemons configured or running.")
			return
		}

		headers := []string{"DAEMON NAME", "COMMAND", "ARGS"}
		var rows [][]string
		for name, d := range daemons {
			argsStr := fmt.Sprintf("%v", d.Args)
			rows = append(rows, []string{
				name,
				d.Command,
				argsStr,
			})
		}

		printTable(headers, rows)
	},
}

func init() {
	mcpCmd.AddCommand(mcpListCmd)
	RootCmd.AddCommand(mcpCmd)
}
