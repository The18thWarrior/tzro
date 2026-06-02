package cli

import (
	"fmt"
	"os"
	"strconv"
	"tzro/internal/memory"

	"github.com/spf13/cobra"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Inspect and manage local behavioral memories and Knowledge Graph",
}

var memoryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all key-value behavioral facts and preferences",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		payload, err := client.GetMemories()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, payload.Facts)
			return
		}

		if len(payload.Facts) == 0 {
			fmt.Println("No behavioral facts found in SQLite.")
			return
		}

		headers := []string{"FACT ID", "TYPE", "CONTENT", "CONFIDENCE", "SOURCE"}
		var rows [][]string
		for _, f := range payload.Facts {
			rows = append(rows, []string{
				f.ID,
				f.Type,
				f.Content,
				strconv.FormatFloat(f.Confidence, 'f', 2, 64),
				f.Source,
			})
		}

		printTable(headers, rows)
	},
}

var memoryAddCmd = &cobra.Command{
	Use:   "add --type [type] --content [content]",
	Short: "Add a manual behavioral preference fact (Connected Mode only)",
	Run: func(cmd *cobra.Command, args []string) {
		memType, _ := cmd.Flags().GetString("type")
		content, _ := cmd.Flags().GetString("content")
		contextInfo, _ := cmd.Flags().GetString("context")

		if memType == "" || content == "" {
			fmt.Fprintln(os.Stderr, "Error: --type and --content are required flags")
			os.Exit(1)
		}

		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		err = client.AddMemory("default", memType, content, contextInfo, 1.0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("✔ Behavioral fact successfully saved and indexed by the daemon observer!")
	},
}

var memoryQueryCmd = &cobra.Command{
	Use:   "query [question]",
	Short: "Run Graph-RAG neighborhood traversal context search",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		question := args[0]

		// In offline mode we query SQLite directly; in connected mode we can fetch and serialize.
		// For consistency and to support direct SQL graph traversal:
		memory.DB.SetDBPathForTesting(globalFlags.DBPath)
		if err := memory.DB.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to connect to local database: %v\n", err)
			os.Exit(1)
		}
		defer memory.DB.Close()

		contextMarkdown := memory.DB.GetGraphRAGContext(question)
		if contextMarkdown == "" {
			fmt.Println("No active relational entities or relationships matched this query.")
			return
		}

		fmt.Println(contextMarkdown)
	},
}

func init() {
	memoryAddCmd.Flags().String("type", "", "Memory type (fact, preference, insight, correction, strategy, anti_pattern)")
	memoryAddCmd.Flags().String("content", "", "Self-contained behavioral fact statement content")
	memoryAddCmd.Flags().String("context", "CLI manual seed", "Details on when/why this memory is created")

	memoryCmd.AddCommand(memoryListCmd)
	memoryCmd.AddCommand(memoryAddCmd)
	memoryCmd.AddCommand(memoryQueryCmd)
	RootCmd.AddCommand(memoryCmd)
}
