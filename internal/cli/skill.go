package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Inspect synthesized Event-Driven Procedural Micro-Skills (Markdown SOPs)",
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all synthesized micro-skills indexed by the engine",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		list, err := client.GetSkills()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, list)
			return
		}

		if len(list) == 0 {
			fmt.Println("No synthesized procedural micro-skills found currently.")
			return
		}

		headers := []string{"SKILL ID", "SOP NAME", "TRIGGER DESCRIPTION", "CREATED DATE"}
		var rows [][]string
		for _, s := range list {
			createdDate := time.Unix(s.CreatedAt, 0).Format("2006-01-02 15:04")
			if s.CreatedAt == 0 {
				createdDate = "Pre-Seeded"
			}
			rows = append(rows, []string{
				s.ID,
				s.Name,
				s.TriggerDescription,
				createdDate,
			})
		}

		printTable(headers, rows)
	},
}

var skillViewCmd = &cobra.Command{
	Use:   "view [skillId]",
	Short: "View raw Markdown SOP procedural contents of a specific skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		skillID := args[0]
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		list, err := client.GetSkills()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		var target *string
		var name string
		for _, s := range list {
			if s.ID == skillID {
				target = &s.SOPContent
				name = s.Name
				break
			}
		}

		if target == nil {
			fmt.Fprintf(os.Stderr, "Error: skill not found: %s\n", skillID)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, map[string]string{"id": skillID, "sopContent": *target})
			return
		}

		fmt.Printf("========================================================================\n")
		fmt.Printf("           PROCEDURAL MICRO-SKILL SOP: %s\n", name)
		fmt.Printf("========================================================================\n\n")
		fmt.Println(*target)
		fmt.Printf("========================================================================\n")
	},
}

func init() {
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillViewCmd)
	RootCmd.AddCommand(skillCmd)
}
