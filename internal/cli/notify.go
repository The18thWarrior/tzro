package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var notifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Inspect durable notifications and alerts (Connected or Offline)",
}

var notifyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all durable notifications and alerts",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		list, err := client.GetNotifications()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, list)
			return
		}

		if len(list) == 0 {
			fmt.Println("No durable notifications found in the log.")
			return
		}

		headers := []string{"ALERT ID", "SOURCE", "TYPE", "TITLE", "MESSAGE", "STATUS"}
		var rows [][]string
		for _, n := range list {
			rows = append(rows, []string{
				n.ID,
				n.Source,
				n.Type,
				n.Title,
				n.Message,
				strings.ToUpper(n.Status),
			})
		}

		printTable(headers, rows)
	},
}

func init() {
	notifyCmd.AddCommand(notifyListCmd)
	RootCmd.AddCommand(notifyCmd)
}
