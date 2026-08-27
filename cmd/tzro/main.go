package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"tzro/pkg/ast"
	"tzro/pkg/compactor"
	"tzro/pkg/hooks"
	"tzro/pkg/probe"
	"tzro/pkg/proxy"
	"tzro/pkg/store"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)
	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575"))
	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5F87"))
)

func getDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tzro", "token_shield.db")
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "tzro",
		Short: "Tzro v2: The Local Token Shield & Context Optimization Engine",
		Long:  `Tzro v2 eliminates cloud API rate limits and token waste on resource-constrained hardware.`,
	}

	// 1. START / PROXY COMMAND
	var port int
	var upstreamAnthropic string
	var upstreamOpenAI string

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the transparent reverse proxy and token shield daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := getDBPath()
			s, err := store.OpenStore(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database at %s: %w", dbPath, err)
			}
			defer s.Close()

			addr := fmt.Sprintf("127.0.0.1:%d", port)
			fmt.Println(titleStyle.Render("🛡️  Tzro v2 — The Local Token Shield"))
			fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Loopback Proxy listening on http://%s", addr)))
			fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Content Store active at %s", dbPath)))
			fmt.Println("\nTo connect your agents, export:")
			fmt.Printf("  export ANTHROPIC_BASE_URL=http://%s\n", addr)
			fmt.Printf("  export OPENAI_BASE_URL=http://%s/v1\n\n", addr)

			srv := proxy.NewServer(proxy.Config{
				ListenAddr:        addr,
				UpstreamAnthropic: upstreamAnthropic,
				UpstreamOpenAI:    upstreamOpenAI,
				Store:             s,
			})

			return srv.Start()
		},
	}
	startCmd.Flags().IntVarP(&port, "port", "p", 7878, "Port to listen on")
	startCmd.Flags().StringVar(&upstreamAnthropic, "upstream-anthropic", "https://api.anthropic.com", "Upstream Anthropic URL")
	startCmd.Flags().StringVar(&upstreamOpenAI, "upstream-openai", "https://api.openai.com", "Upstream OpenAI URL")

	// 2. PROBE COMMAND
	probeCmd := &cobra.Command{
		Use:   "probe [query]",
		Short: "Fast local codebase discovery using ripgrep + Tree-sitter AST",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			cwd, _ := os.Getwd()
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}

			report, err := probe.Probe(cwd, query, 20, s)
			if err != nil {
				return err
			}

			fmt.Println(report.FormatMarkdown())
			return nil
		},
	}

	// 3. SKELETON COMMAND
	skeletonCmd := &cobra.Command{
		Use:   "skeleton [file]",
		Short: "Generate AST skeleton with body hashes for a source file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			content, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}

			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}

			res, err := ast.Skeletonize(filePath, content, s)
			if err != nil {
				return err
			}

			fmt.Println(res.SkeletonCode)
			fmt.Fprintf(os.Stderr, "\n%s\n", infoStyle.Render(fmt.Sprintf("✓ Compacted: %d bytes -> %d bytes (%.1f%% reduction, %d bodies saved)",
				res.OriginalSize, res.SkeletonSize, res.SavingsRatio*100, res.ElidedBlocks)))
			return nil
		},
	}

	// 4. EXPAND COMMAND
	expandCmd := &cobra.Command{
		Use:   "expand [hash]",
		Short: "Retrieve original code body by its content hash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hash := args[0]
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return err
			}
			defer s.Close()

			blob, err := s.GetBlob(hash)
			if err != nil {
				return err
			}

			fmt.Printf("// %s (Lines %d-%d)\n", blob.FilePath, blob.StartLine, blob.EndLine)
			fmt.Println(blob.Body)
			return nil
		},
	}

	// 5. COMPACT COMMAND
	compactCmd := &cobra.Command{
		Use:   "compact",
		Short: "Read raw test logs or JSON arrays from stdin and print compacted output",
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			compacted := compactor.CompactLog(string(input))
			fmt.Print(compacted)
			return nil
		},
	}

	// 6. HOOK COMMAND (Antigravity, Claude, Hermes, Copilot, Pi-Coder Bridge)
	hookCmd := &cobra.Command{
		Use:   "hook [harness] [event]",
		Short: "Agent lifecycle hook bridge for Antigravity, Claude Code, Hermes, Copilot, and Pi-Coder",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			harness := "antigravity"
			event := args[0]
			if len(args) == 2 {
				harness = strings.ToLower(args[0])
				event = strings.ToLower(args[1])
			} else if args[0] == "compact" {
				return hooks.HandleCompactOutput(os.Stdin, os.Stdout)
			}

			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}

			switch harness {
			case "claude", "claude-code":
				switch event {
				case "pre-tool", "pre_tool", "PreToolUse":
					return hooks.HandleClaudePreToolUse(os.Stdin, os.Stdout, s)
				case "post-tool", "post_tool", "PostToolUse":
					return hooks.HandleClaudePostToolUse(os.Stdin, os.Stdout, s)
				default:
					return fmt.Errorf("unknown claude hook event: %s", event)
				}

			case "hermes":
				switch event {
				case "pre-tool", "pre_tool", "pre_tool_call":
					return hooks.HandleHermesPreTool(os.Stdin, os.Stdout, s)
				case "post-tool", "post_tool", "post_tool_call":
					return hooks.HandleHermesPostTool(os.Stdin, os.Stdout, s)
				default:
					return fmt.Errorf("unknown hermes hook event: %s", event)
				}

			case "copilot", "github-copilot":
				switch event {
				case "pre-tool", "pre_tool":
					return hooks.HandleCopilotPreTool(os.Stdin, os.Stdout, s)
				case "post-tool", "post_tool":
					return hooks.HandleCopilotPostTool(os.Stdin, os.Stdout, s)
				default:
					return fmt.Errorf("unknown copilot hook event: %s", event)
				}

			case "pi-coder", "picoder":
				switch event {
				case "pre-tool", "pre_tool":
					return hooks.HandlePiCoderPreTool(os.Stdin, os.Stdout, s)
				case "post-tool", "post_tool":
					return hooks.HandlePiCoderPostTool(os.Stdin, os.Stdout, s)
				default:
					return fmt.Errorf("unknown pi-coder hook event: %s", event)
				}

			case "antigravity", "default":
				switch event {
				case "pre-tool", "pre_tool", "PreToolUse":
					return hooks.HandlePreToolUse(os.Stdin, os.Stdout, s)
				case "post-tool", "post_tool", "PostToolUse":
					return hooks.HandlePostToolUse(os.Stdin, os.Stdout, s)
				case "compact":
					return hooks.HandleCompactOutput(os.Stdin, os.Stdout)
				default:
					return fmt.Errorf("unknown antigravity hook event: %s", event)
				}

			default:
				return fmt.Errorf("unknown harness: %s (expected antigravity, claude, hermes, copilot, or pi-coder)", harness)
			}
		},
	}

	// 7. INIT COMMAND (Configure hooks & environments)
	var hookTargets []string
	var isWorkspace bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize and configure lifecycle hooks for AI coding agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(titleStyle.Render("⚡ Tzro Agent Lifecycle Hook Initializer"))
			results, err := hooks.DetectAndInstallHooks(hookTargets, isWorkspace)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Println(warnStyle.Render("No active agent environments detected."))
				fmt.Println("Run with `--hooks all` or `--hooks claude,antigravity,hermes,copilot` to force configuration.")
				return nil
			}

			for _, r := range results {
				if strings.HasPrefix(r.Status, "failed") {
					fmt.Printf("  %s %s: %s\n", warnStyle.Render("✗"), lipgloss.NewStyle().Bold(true).Render(string(r.Harness)), r.Status)
				} else {
					fmt.Printf("  %s %s: %s (%s)\n", infoStyle.Render("✔"), lipgloss.NewStyle().Bold(true).Render(string(r.Harness)), r.Status, r.ConfigPath)
				}
			}
			fmt.Println(infoStyle.Render("\n✔ Lifecycle hooks successfully configured."))
			return nil
		},
	}
	initCmd.Flags().StringSliceVar(&hookTargets, "hooks", []string{"auto"}, "Agent hook targets to configure: auto, all, antigravity, claude, hermes, copilot, pi-coder")
	initCmd.Flags().BoolVarP(&isWorkspace, "workspace", "w", false, "Configure hooks in current workspace instead of user home directory")

	// 8. STATUS COMMAND
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check running token shield metrics and memory footprint",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
			if err != nil {
				fmt.Println(warnStyle.Render("● Tzro Token Shield is NOT running on localhost:7878"))
				fmt.Println("Run `tzro start` to launch the daemon.")
				return nil
			}
			defer resp.Body.Close()

			var m proxy.Metrics
			if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
				return err
			}

			fmt.Println(titleStyle.Render("🛡️  Tzro Token Shield Status"))
			fmt.Printf("  Status:            %s\n", infoStyle.Render("ACTIVE (Running)"))
			fmt.Printf("  Total Requests:    %d\n", m.TotalRequests)
			fmt.Printf("  Anthropic Turns:   %d\n", m.AnthropicRequests)
			fmt.Printf("  OpenAI Turns:      %d\n", m.OpenAIRequests)
			fmt.Printf("  Bytes Shielded:    %d bytes\n", m.BytesProcessed)
			fmt.Printf("  Secrets Redacted:  %d\n", m.SecretsRedacted)
			fmt.Printf("  Memory (RSS):      %d MB\n", m.MemoryAllocMB)
			fmt.Printf("  Uptime:            %d seconds\n", m.UptimeSeconds)
			return nil
		},
	}
	statusCmd.Flags().IntVarP(&port, "port", "p", 7878, "Port to query")

	// 10. INGEST COMMAND
	var ingestTableName string
	ingestCmd := &cobra.Command{
		Use:   "ingest [file]",
		Short: "Import tabular data (CSV/TSV/JSON) into SQLite for agent queries",
		Long: `Read tabular data from a file or stdin, detect the format (CSV, TSV, or JSON array),
import it into the local SQLite store, and print a data envelope with sample rows
and a table pointer the agent can use with 'tzro query'.

Examples:
  tzro ingest data.csv
  tzro ingest results.json --name my_results
  cat report.tsv | tzro ingest -
  curl -s https://api.example.com/data | tzro ingest -`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var data []byte
			var err error

			if len(args) == 0 || args[0] == "-" {
				// Read from stdin
				data, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("failed to read stdin: %w", err)
				}
			} else {
				// Read from file
				data, err = os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("failed to read file %s: %w", args[0], err)
				}
			}

			if len(data) == 0 {
				return fmt.Errorf("no input data provided")
			}

			td, ok := compactor.DetectTabular(string(data))
			if !ok {
				return fmt.Errorf("input does not appear to be tabular data (CSV, TSV, or JSON array)")
			}

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			// Determine table name
			tableName := ingestTableName
			if tableName == "" {
				// Auto-generate from content hash
				sampleSize := 3
				if len(td.Rows) < sampleSize {
					sampleSize = len(td.Rows)
				}
				var parts []string
				parts = append(parts, strings.Join(td.Columns, "|"))
				for i := 0; i < sampleSize; i++ {
					parts = append(parts, strings.Join(td.Rows[i], "|"))
				}
				tableName = "tbl_" + store.ComputeHash(strings.Join(parts, "\n"))
			}

			if err := s.ImportTabular(tableName, td.Columns, td.Rows); err != nil {
				return fmt.Errorf("import failed: %w", err)
			}

			fmt.Print(compactor.FormatEnvelope(tableName, td, 5))
			return nil
		},
	}
	ingestCmd.Flags().StringVarP(&ingestTableName, "name", "n", "", "Custom table name (default: auto-generated from content hash)")

	// 11. QUERY COMMAND
	queryCmd := &cobra.Command{
		Use:   "query [table] [sql]",
		Short: "Execute a read-only SQL query against an imported tabular data table",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tableName := args[0]
			sqlQuery := args[1]

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			results, cols, err := s.QuerySQL(sqlQuery)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Printf("No results from table %s.\n", tableName)
				return nil
			}

			// Format results as compact markdown table
			fmt.Printf("# Query Results (%d rows)\n", len(results))
			fmt.Printf("| %s |\n", strings.Join(cols, " | "))
			fmt.Printf("|%s\n", strings.Repeat(" --- |", len(cols)))
			for _, row := range results {
				var vals []string
				for _, col := range cols {
					vals = append(vals, row[col])
				}
				fmt.Printf("| %s |\n", strings.Join(vals, " | "))
			}
			return nil
		},
	}

	rootCmd.AddCommand(startCmd, probeCmd, skeletonCmd, expandCmd, compactCmd, hookCmd, initCmd, statusCmd, queryCmd, ingestCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
