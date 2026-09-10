package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"tzro/pkg/ast"
	"tzro/pkg/benchmark/signaldensity"
	"tzro/pkg/compactor"
	tzroctx "tzro/pkg/context"
	"tzro/pkg/dlp"
	"tzro/pkg/doctor"
	"tzro/pkg/hooks"
	"tzro/pkg/inspector"
	"tzro/pkg/kvlock"
	"tzro/pkg/probe"
	"tzro/pkg/proxy"
	"tzro/pkg/search"
	"tzro/pkg/session"
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

func newRootCmd() *cobra.Command {
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

			// Load workspace privacy policy (fail-fast on parse errors)
			cwd, _ := os.Getwd()
			wp, err := dlp.LoadWorkspacePolicy(cwd)
			if err != nil {
				return fmt.Errorf("privacy policy error: %w", err)
			}
			policy := dlp.NewPolicyEngine(wp)

			fmt.Println(titleStyle.Render("🛡️  Tzro v2 — The Local Token Shield"))
			fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Loopback Proxy listening on http://%s", addr)))
			fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Content Store active at %s", dbPath)))
			fmt.Println(infoStyle.Render("✓ Privacy policy loaded"))
			fmt.Println("\nTo connect your agents, export:")
			fmt.Printf("  export ANTHROPIC_BASE_URL=http://%s\n", addr)
			fmt.Printf("  export OPENAI_BASE_URL=http://%s/v1\n\n", addr)

			srv := proxy.NewServer(proxy.Config{
				ListenAddr:        addr,
				UpstreamAnthropic: upstreamAnthropic,
				UpstreamOpenAI:    upstreamOpenAI,
				Store:             s,
				Policy:            policy,
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

			res, err := ast.Skeletonize(filePath, content, s, "")
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
	var linesRange string
	expandCmd := &cobra.Command{
		Use:   "expand [hash-or-artifact-id]",
		Short: "Retrieve original code body or stored artifact content with optional line ranges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idOrHash := args[0]
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return err
			}
			defer s.Close()

			var fullContent string
			var header string

			// Check if it's an artifact ID
			if strings.HasPrefix(idOrHash, "art_") {
				art, err := s.GetArtifact(idOrHash, "")
				if err != nil {
					return fmt.Errorf("artifact retrieval failed: %w", err)
				}
				fullContent = art.Body
				header = fmt.Sprintf("// Artifact: %s (%s, %d bytes)", art.ID, art.Type, art.SizeBytes)
				if art.IsRedacted {
					header += " [Note: Modified original with redacted secrets]"
				}
			} else {
				// Otherwise retrieve from content blobs
				blob, err := s.GetBlob(idOrHash)
				if err != nil {
					return err
				}
				fullContent = blob.Body
				header = fmt.Sprintf("// %s (Lines %d-%d)", blob.FilePath, blob.StartLine, blob.EndLine)
			}

			// Apply line slice if --lines flag provided (e.g. 10-25)
			if linesRange != "" {
				var start, end int
				_, err := fmt.Sscanf(linesRange, "%d-%d", &start, &end)
				if err != nil || start <= 0 || end < start {
					return fmt.Errorf("invalid line range format %q (expected format <start>-<end>, e.g. 10-25)", linesRange)
				}

				allLines := strings.Split(fullContent, "\n")
				if start > len(allLines) {
					return fmt.Errorf("start line %d exceeds file length (%d lines)", start, len(allLines))
				}
				if end > len(allLines) {
					end = len(allLines)
				}
				slicedLines := allLines[start-1 : end]
				fmt.Printf("%s [Lines %d-%d]\n", header, start, end)
				fmt.Println(strings.Join(slicedLines, "\n"))
				return nil
			}

			fmt.Println(header)
			fmt.Println(fullContent)
			return nil
		},
	}
	expandCmd.Flags().StringVar(&linesRange, "lines", "", "Line range to expand, e.g. 10-50")


	// 5. COMPACT COMMAND
	var compactRunCmd string
	var compactFormat string

	compactCmd := &cobra.Command{
		Use:   "compact",
		Short: "Read raw test logs, or run a command, and emit compacted output with evidence guarantees",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}
			wp, _ := dlp.LoadWorkspacePolicy(cwd)
			policy := dlp.NewPolicyEngine(wp)

			var evidenceRes *compactor.CompactedEvidence
			var err error

			if compactRunCmd != "" {
				evidenceRes, err = compactor.RunAndCompact(compactRunCmd, cwd, s, policy)
				if err != nil {
					return err
				}
			} else {
				input, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				evidenceRes, err = compactor.CompactEvidence(string(input), cwd, s, policy, nil)
				if err != nil {
					return err
				}
			}

			if compactFormat == "json" {
				b, err := json.MarshalIndent(evidenceRes, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
			} else {
				if len(evidenceRes.Diagnostics) > 0 {
					fmt.Print(evidenceRes.FormatMarkdown())
				} else if evidenceRes.RawOutput != "" {
					if evidenceRes.ArtifactRef != "" {
						fmt.Printf("// [Tzro Artifact: %s | Full original retained (run `tzro expand %s` to retrieve)]\n", evidenceRes.ArtifactRef, evidenceRes.ArtifactRef)
					}
					fmt.Println(evidenceRes.RawOutput)
				} else {
					fmt.Print(evidenceRes.FormatMarkdown())
				}
			}

			if compactRunCmd != "" && evidenceRes.ExitCode != 0 {
				os.Exit(evidenceRes.ExitCode)
			}
			return nil
		},
	}
	compactCmd.Flags().StringVar(&compactRunCmd, "run", "", "Command to execute and compact in wrapper mode")
	compactCmd.Flags().StringVar(&compactFormat, "format", "markdown", "Output format: markdown|json")

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
			fmt.Printf("  Status:               %s\n", infoStyle.Render("ACTIVE (Running)"))
			fmt.Printf("  Total Requests:       %d\n", m.TotalRequests)
			fmt.Printf("  Anthropic Turns:      %d\n", m.AnthropicRequests)
			fmt.Printf("  OpenAI Turns:         %d\n", m.OpenAIRequests)
			fmt.Printf("  Responses Turns:      %d\n", m.ResponsesRequests)
			fmt.Printf("  Gemini Turns:         %d\n", m.GeminiRequests)
			fmt.Printf("  Local Turns:          %d\n", m.LocalRequests)
			fmt.Printf("  Bytes Shielded:       %d bytes\n", m.BytesProcessed)
			fmt.Printf("  Secrets Redacted:     %d\n", m.SecretsRedacted)
			fmt.Printf("  Memory (RSS):         %d MB\n", m.MemoryAllocMB)
			fmt.Printf("  Uptime:               %d seconds\n\n", m.UptimeSeconds)

			fmt.Println(lipgloss.NewStyle().Bold(true).Render("📊 Token Telemetry (Measured from Providers):"))
			formatVal := func(p *int64) string {
				if p == nil {
					return "unknown"
				}
				return fmt.Sprintf("%d tokens", *p)
			}
			fmt.Printf("  Prompt / Input:       %s\n", formatVal(m.MeasuredInputTokens))
			fmt.Printf("  Completion / Output:  %s\n", formatVal(m.MeasuredOutputTokens))
			fmt.Printf("  Total Observed:       %s\n", formatVal(m.MeasuredTotalTokens))
			fmt.Printf("  Native Provider Cache:%s\n", formatVal(m.NativeCacheHitTokens))
			fmt.Printf("  Tzro Prefix Locked:   %s\n", formatVal(m.TzroPrefixLockTokens))

			// Artifact storage stats
			dbPath := getDBPath()
			if st, err := store.OpenStore(dbPath); err == nil {
				defer st.Close()
				cwd, _ := os.Getwd()
				count, totalBytes, err := st.GetWorkspaceArtifactStats(cwd)
				if err == nil {
					fmt.Printf("\n")
					fmt.Println(lipgloss.NewStyle().Bold(true).Render("📦 Artifact Storage:"))
					fmt.Printf("  Workspace:            %s\n", cwd)
					fmt.Printf("  Artifact Count:       %d / %d\n", count, store.DefaultWorkspaceMaxCount)
					fmt.Printf("  Disk Usage:           %s / %s\n", formatBytes(totalBytes), formatBytes(store.DefaultWorkspaceMaxBytes))
				}
			}

			return nil
		},
	}
	statusCmd.Flags().IntVarP(&port, "port", "p", 7878, "Port to query")

	// 9. DOCTOR COMMAND (Synthetic handshake & endpoint inspection)
	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run synthetic health checks, provider route diagnostics, and hook verification",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(titleStyle.Render("🩺 Tzro Diagnostic Doctor & Route Inspection"))
			fmt.Println("Inspecting local loopback proxy, upstream providers, and registered hook harnesses...")
			fmt.Println()

			// Check 1: Live Route Probing (replaces static hardcoded matrix)
			proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			routeReports := doctor.ProbeRoutes(proxyURL)

			// Determine proxy status from route reports
			proxyOnline := false
			for _, rr := range routeReports {
				if rr.Status == doctor.RouteStatusActive {
					proxyOnline = true
					break
				}
			}

			if proxyOnline {
				fmt.Printf("  %s Loopback Proxy: ACTIVE on %s\n", infoStyle.Render("✔"), proxyURL)
			} else {
				fmt.Printf("  %s Loopback Proxy: NOT RUNNING on %s\n", warnStyle.Render("✗"), proxyURL)
			}

			// Check 2: Route Support Diagnostic Matrix (Live Probed)
			fmt.Println(lipgloss.NewStyle().Bold(true).Render("\n📡 Provider Route & Interception Matrix (Live Probed):"))
			for _, rr := range routeReports {
				switch rr.Status {
				case doctor.RouteStatusActive:
					features := "KV-Lock + DLP"
					if len(rr.Features) > 0 {
						features = strings.Join(rr.Features, ", ")
					}
					fmt.Printf("  %s %-30s [%s] -> ACTIVE (%s, %.1fms)\n",
						infoStyle.Render("✔"), rr.Route, rr.Adapter, features,
						float64(rr.Latency.Microseconds())/1000.0)
				case doctor.RouteStatusOffline:
					fmt.Printf("  %s %-30s [%s] -> OFFLINE (Proxy daemon stopped)\n",
						warnStyle.Render("✗"), rr.Route, rr.Adapter)
				case doctor.RouteStatusUnsupported:
					fmt.Printf("  %s %-30s [%s] -> UNSUPPORTED (Direct pass-through / unintercepted)\n",
						warnStyle.Render("!"), rr.Route, rr.Adapter)
				}
			}

			if !proxyOnline {
				fmt.Printf("\n  %s Run 'tzro start' to launch the proxy daemon and activate route interception.\n",
					warnStyle.Render("→"))
			}

			// Check 3: Upstream Provider Connectivity (DNS & TLS Handshake)
			fmt.Println(lipgloss.NewStyle().Bold(true).Render("\n🌐 Upstream Provider Connectivity (DNS & TLS Handshake):"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			upstreamReports := doctor.ProbeUpstreams(ctx, doctor.DefaultUpstreams())
			for _, ur := range upstreamReports {
				switch ur.Status {
				case doctor.UpstreamStatusReachable:
					tlsInfo := ""
					if ur.TLSValid {
						tlsInfo = "TLS verified, "
					}
					fmt.Printf("  %s %-25s (%s) -> REACHABLE (%s%dms)\n",
						infoStyle.Render("✔"), ur.Provider, ur.Endpoint, tlsInfo,
						ur.Latency.Milliseconds())
				case doctor.UpstreamStatusOffline:
					detail := "not detected"
					if ur.Error != "" {
						detail = ur.Error
					}
					fmt.Printf("  %s %-25s (%s) -> OFFLINE (%s)\n",
						warnStyle.Render("!"), ur.Provider, ur.Endpoint, detail)
				case doctor.UpstreamStatusUnreachable:
					fmt.Printf("  %s %-25s (%s) -> UNREACHABLE (%s)\n",
						warnStyle.Render("✗"), ur.Provider, ur.Endpoint, ur.Error)
				}
			}

			// Check 4: Lifecycle Hooks Diagnostic
			fmt.Println(lipgloss.NewStyle().Bold(true).Render("\n🪝 Agent Lifecycle Hook Verification:"))
			results, err := hooks.DetectAndInstallHooks([]string{"auto"}, false)
			if err != nil {
				fmt.Printf("  %s Hook detection error: %v\n", warnStyle.Render("✗"), err)
			} else if len(results) == 0 {
				fmt.Printf("  %s No agent environments auto-detected in default paths.\n", warnStyle.Render("!"))
			} else {
				for _, r := range results {
					fmt.Printf("  %s %s: %s (%s)\n", infoStyle.Render("✔"), r.Harness, r.Status, r.ConfigPath)
				}
			}

			// Check 5: Synthetic KV-Lock & DLP Normalization Handshake
			fmt.Println(lipgloss.NewStyle().Bold(true).Render("\n⚡ Synthetic Normalization & Redaction Handshake:"))
			testGuard := kvlock.NewLockGuard()
			_, hash, err := testGuard.NormalizeOpenAI([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"ping"}]}`))
			if err != nil || hash == "" {
				fmt.Printf("  %s KV-Lock Normalization: FAILED (%v)\n", warnStyle.Render("✗"), err)
			} else {
				fmt.Printf("  %s KV-Lock Normalization: PASS (deterministic prefix hash: %s)\n", infoStyle.Render("✔"), hash)
			}

			testRedactor := dlp.NewRedactor()
			redacted, dlpMap := testRedactor.Redact("test sk-proj-1234567890abcdef1234567890abcdef sample")
			if len(dlpMap) == 0 || !strings.Contains(redacted, "[REDACTED_") {
				fmt.Printf("  %s DLP Redaction Pipeline: FAILED\n", warnStyle.Render("✗"))
			} else {
				fmt.Printf("  %s DLP Redaction Pipeline: PASS (%d secret(s) redacted)\n", infoStyle.Render("✔"), len(dlpMap))
			}

			// Check 6: Local SQLite Store & FTS5 Capability
			fmt.Println(lipgloss.NewStyle().Bold(true).Render("\n🗄️  Local Storage & Search Engine Check:"))
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				fmt.Printf("  %s SQLite Store Init: FAILED (%v)\n", warnStyle.Render("✗"), err)
			} else {
				defer s.Close()
				if s.HasFTS5() {
					fmt.Printf("  %s SQLite FTS5 Engine: ACTIVE (BM25 full-text indexing enabled)\n", infoStyle.Render("✔"))
				} else {
					fmt.Printf("  %s SQLite FTS5 Engine: UNAVAILABLE (falling back to standard lexical index. Ensure CGO_ENABLED=1 and SQLite 3.9+ with FTS5)\n", warnStyle.Render("!"))
				}
				fmt.Printf("  %s Database Path: %s\n", infoStyle.Render("✔"), getDBPath())
			}

			fmt.Println(infoStyle.Render("\n✔ Doctor inspection completed."))
			return nil
		},
	}
	doctorCmd.Flags().IntVarP(&port, "port", "p", 7878, "Port to query")

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
		Use:   "query [table-or-artifact-id] [sql]",
		Short: "Execute a read-only SQL query against an imported tabular table or stored artifact",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			sqlQuery := args[1]

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			tableName := target
			var sourceArtifactID string

			// If target is an artifact ID, verify and import if needed
			if strings.HasPrefix(target, "art_") {
				art, err := s.GetArtifact(target, "")
				if err != nil {
					return fmt.Errorf("tabular artifact %s not found: %w", target, err)
				}
				sourceArtifactID = art.ID
				tableName = "tbl_" + art.Hash[:12]
				td, ok := compactor.DetectTabular(art.Body)
				if ok {
					_ = s.ImportTabular(tableName, td.Columns, td.Rows)
				}
				// Replace references to artifact ID with actual table name in SQL query
				sqlQuery = strings.ReplaceAll(sqlQuery, target, tableName)
			}

			results, cols, err := s.QuerySQL(sqlQuery)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Printf("No results from %s.\n", target)
				return nil
			}

			// Format results as compact markdown table
			title := fmt.Sprintf("# Query Results (%d rows from %s)\n", len(results), target)
			if sourceArtifactID != "" {
				title += fmt.Sprintf("// Provenance: Source Artifact ID: %s\n", sourceArtifactID)
			}
			fmt.Print(title)
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


	// 12. DLP PREVIEW COMMAND
	dlpCmd := &cobra.Command{
		Use:   "dlp",
		Short: "Zero-Cloud DLP and privacy policy tools",
	}

	dlpPreviewCmd := &cobra.Command{
		Use:   "preview [file/path]",
		Short: "Dry-run preview of DLP redactions and policy actions across files without modifying data",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			targetPath := cwd
			if len(args) > 0 {
				targetPath = args[0]
			}

			policy, err := dlp.LoadWorkspacePolicy(cwd)
			if err != nil {
				return err
			}
			engine := dlp.NewPolicyEngine(policy)
			redactor := dlp.NewRedactor()

			fmt.Println(titleStyle.Render("🛡️  Tzro Zero-Cloud DLP Policy Preview"))
			fmt.Println(lipgloss.NewStyle().Faint(true).Render("Notice: Application-level DLP interception is not an OS-wide packet firewall.\n"))

			info, err := os.Stat(targetPath)
			if err != nil {
				return err
			}

			scanFile := func(path string) {
				eval := engine.EvaluatePath(path)
				rel, _ := filepath.Rel(cwd, path)
				if rel == "" {
					rel = path
				}

				if !eval.Allowed {
					fmt.Printf("  %s %s: %s (%s)\n", warnStyle.Render("BLOCKED"), rel, eval.Reason, eval.Action)
					return
				}

				data, err := os.ReadFile(path)
				if err != nil {
					return
				}

				// Content evaluation
				contentEval := engine.EvaluateContent(string(data))
				if !contentEval.Allowed {
					fmt.Printf("  %s %s: %s\n", warnStyle.Render("BLOCKED"), rel, contentEval.Reason)
					return
				}

				// Redaction preview
				redacted, mapping := redactor.Redact(string(data))
				if len(mapping) > 0 {
					fmt.Printf("  %s %s (%d secrets masked)\n", infoStyle.Render("REDACTED"), rel, len(mapping))
					for placeholder := range mapping {
						fmt.Printf("    ↳ %s\n", placeholder)
					}
					_ = redacted
				} else {
					fmt.Printf("  %s %s (clean)\n", lipgloss.NewStyle().Faint(true).Render("ALLOWED"), rel)
				}
			}

			if !info.IsDir() {
				scanFile(targetPath)
			} else {
				_ = filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						if d != nil && (d.Name() == ".git" || d.Name() == "node_modules") {
							return filepath.SkipDir
						}
						return nil
					}
					scanFile(path)
					return nil
				})
			}

			return nil
		},
	}

	dlpCmd.AddCommand(dlpPreviewCmd)

	// IMPACT COMMAND
	var impactBudget int
	var impactStaged bool
	var impactUnstaged bool
	var impactAll bool
	var impactSymbol string
	var impactIncludeGenerated bool
	var impactFormat string
	var impactRevision string

	impactCmd := &cobra.Command{
		Use:   "impact",
		Short: "Assemble change-impact context packs from git diff or symbol with coverage guarantees",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}
			wp, _ := dlp.LoadWorkspacePolicy(cwd)
			policy := dlp.NewPolicyEngine(wp)

			analyzer := tzroctx.NewImpactAnalyzer(s, policy)

			var pack *tzroctx.ContextPack
			var err error

			if impactSymbol != "" {
				pack, err = analyzer.AnalyzeSymbol(cwd, impactSymbol, impactBudget, impactIncludeGenerated)
			} else {
				var gitArgs []string
				if impactStaged {
					gitArgs = []string{"diff", "--cached"}
				} else if impactUnstaged {
					gitArgs = []string{"diff"}
				} else {
					gitArgs = []string{"diff", "HEAD"}
				}

				gitCmd := exec.Command("git", gitArgs...)
				gitCmd.Dir = cwd
				diffBytes, gitErr := gitCmd.Output()
				diffText := string(diffBytes)

				if gitErr != nil || strings.TrimSpace(diffText) == "" {
					gitCmd2 := exec.Command("git", "diff")
					gitCmd2.Dir = cwd
					diffBytes2, _ := gitCmd2.Output()
					diffText = string(diffBytes2)
				}

				if strings.TrimSpace(diffText) == "" {
					fmt.Println(warnStyle.Render("No working-tree diff detected. Use --symbol <name> to analyze a specific symbol."))
					return nil
				}

				pack, err = analyzer.AnalyzeDiff(cwd, diffText, impactBudget, impactIncludeGenerated)
			}

			if err != nil {
				return err
			}

			if impactFormat == "json" {
				b, err := json.MarshalIndent(pack, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
			} else {
				fmt.Print(pack.FormatMarkdown())
			}

			return nil
		},
	}
	impactCmd.Flags().IntVar(&impactBudget, "budget", 4000, "Token budget for context pack")
	impactCmd.Flags().BoolVar(&impactStaged, "staged", false, "Analyze staged changes only")
	impactCmd.Flags().BoolVar(&impactUnstaged, "unstaged", false, "Analyze unstaged changes only")
	impactCmd.Flags().BoolVar(&impactAll, "all", true, "Analyze all working tree changes (staged + unstaged)")
	impactCmd.Flags().StringVar(&impactSymbol, "symbol", "", "Symbol fallback to analyze")
	impactCmd.Flags().BoolVar(&impactIncludeGenerated, "include-generated", false, "Include generated code files")
	impactCmd.Flags().StringVar(&impactFormat, "format", "markdown", "Output format: markdown|json")
	impactCmd.Flags().StringVar(&impactRevision, "revision", "", "Revision range (reserved)")

	// SEARCH COMMAND (Unified Local Evidence Search)
	var searchBudget int
	var searchFormat string
	searchCmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Unified local evidence search across code, docs, configs, logs, sessions, and data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			cwd, _ := os.Getwd()
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}
			wp, _ := dlp.LoadWorkspacePolicy(cwd)
			policy := dlp.NewPolicyEngine(wp)

			engine := search.NewEngine(s, policy)
			res, err := engine.Search(cwd, query, searchBudget)
			if err != nil {
				return err
			}

			if searchFormat == "json" {
				b, err := json.MarshalIndent(res, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
			} else {
				fmt.Print(res.FormatMarkdown())
			}
			return nil
		},
	}
	searchCmd.Flags().IntVar(&searchBudget, "budget", 4000, "Token budget for search results")
	searchCmd.Flags().StringVar(&searchFormat, "format", "markdown", "Output format: markdown|json")

	// 13. CONTEXT COMMAND
	var contextBudget int
	contextCmd := &cobra.Command{
		Use:   "context [query]",
		Short: "Assemble a ranked, token-budgeted context pack for a task query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			cwd, _ := os.Getwd()
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}

			assembler := tzroctx.NewAssembler(s, nil)
			pack, err := assembler.Assemble(cwd, query, contextBudget)
			if err != nil {
				return err
			}

			fmt.Print(pack.FormatMarkdown())
			return nil
		},
	}

	// 14. INSPECT COMMAND GROUP
	inspectCmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect context assembly decisions and perform offline quality replays",
	}

	inspectListCmd := &cobra.Command{
		Use:   "list",
		Short: "List recent context assembly traces for the current workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			traces, err := s.ListContextTraces(cwd, 20)
			if err != nil {
				return err
			}
			if len(traces) == 0 {
				fmt.Println(infoStyle.Render("No context assembly traces recorded for this workspace."))
				return nil
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("🔍 Context Traces (%d recorded)", len(traces))))
			for _, tr := range traces {
				fmt.Printf("- `%s` | Query: %q | Budget: %d | Size: %s | %s\n",
					tr.ID, tr.Query, tr.Budget, formatBytes(tr.SizeBytes), tr.CreatedAt.Format(time.RFC3339))
			}
			return nil
		},
	}

	inspectShowCmd := &cobra.Command{
		Use:   "show [trace-id]",
		Short: "Display full six-stage diagnostic trace breakdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]
			cwd, _ := os.Getwd()
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			wp, _ := dlp.LoadWorkspacePolicy(cwd)
			policy := dlp.NewPolicyEngine(wp)

			engine := inspector.NewEngine(s, policy)
			md, err := engine.ExportTraceMarkdown(traceID, cwd)
			if err != nil {
				return err
			}
			fmt.Print(md)
			return nil
		},
	}

	inspectExplainCmd := &cobra.Command{
		Use:   "explain [trace-id] [candidate-path]",
		Short: "Explain why a specific candidate was included or omitted in a trace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]
			candidatePath := args[1]
			cwd, _ := os.Getwd()

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			engine := inspector.NewEngine(s, nil)
			exp, err := engine.ExplainCandidate(traceID, cwd, candidatePath)
			if err != nil {
				return err
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("🔎 Explanation for `%s` in %s", candidatePath, traceID)))
			fmt.Printf("- **Stage:** %s\n", exp.Stage)
			fmt.Printf("- **Reason:** %s\n", exp.Reason)
			if exp.Rank > 0 {
				fmt.Printf("- **Rank:** %d\n", exp.Rank)
			}
			fmt.Printf("- **Evidence Tier:** %s\n", exp.Tier)
			return nil
		},
	}

	var replayBudget int
	inspectReplayCmd := &cobra.Command{
		Use:   "replay [trace-id]",
		Short: "Replay context ranking and packing under alternative parameters without disk access",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]
			cwd, _ := os.Getwd()

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			engine := inspector.NewEngine(s, nil)
			res, err := engine.Replay(traceID, cwd, replayBudget)
			if err != nil {
				return err
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("🔄 Offline Snapshot Replay: %s (Budget: %d tokens)", traceID, replayBudget)))
			fmt.Printf("Used Tokens: %d\n\n", res.TokensUsed)

			fmt.Printf("### Included Items (%d):\n", len(res.IncludedItems))
			for i, it := range res.IncludedItems {
				fmt.Printf("%d. `%s` (%d tokens) [%s]\n", i+1, it.Path, it.TokensUsed, it.Tier)
			}

			if len(res.TruncatedManifest) > 0 {
				fmt.Printf("\n### Truncated Candidates (%d):\n", len(res.TruncatedManifest))
				for _, p := range res.TruncatedManifest {
					fmt.Printf("- `%s` [%s]\n", p, inspector.TierCounterfactual)
				}
			}
			return nil
		},
	}
	inspectReplayCmd.Flags().IntVar(&replayBudget, "budget", 8000, "Counterfactual token budget to test")

	var outcomeDataStr string
	inspectOutcomeCmd := &cobra.Command{
		Use:   "outcome [trace-id]",
		Short: "Attach external evaluation outcome data to a trace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceID := args[0]
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			if outcomeDataStr == "" {
				// Query outcome
				engine := inspector.NewEngine(s, nil)
				out, err := engine.GetOutcome(traceID)
				if err != nil {
					return err
				}
				if len(out) == 0 {
					fmt.Println(infoStyle.Render("No external outcome data attached to trace."))
					return nil
				}
				b, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(b))
				return nil
			}

			if err := s.PutTraceOutcome(traceID, outcomeDataStr); err != nil {
				return err
			}
			fmt.Println(infoStyle.Render("✓ Attached evaluation outcome to trace."))
			return nil
		},
	}
	inspectOutcomeCmd.Flags().StringVar(&outcomeDataStr, "data", "", "JSON outcome data to attach")

	inspectCmd.AddCommand(inspectListCmd, inspectShowCmd, inspectExplainCmd, inspectReplayCmd, inspectOutcomeCmd)
	// 14. SESSION COMMAND
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage portable agent session handoffs",
	}

	var sessionObjective string
	var sessionBranch string
	var sessionExportFile string
	var sessionDecisions []string
	var sessionConstraints []string
	var sessionPending []string
	var sessionFiles []string
	sessionSaveCmd := &cobra.Command{
		Use:   "save [session-id]",
		Short: "Save current session state to local SQLite store and generate Markdown handoff",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			cwd, _ := os.Getwd()

			// Auto-sense branch from git if not explicitly provided
			if sessionBranch == "" {
				sessionBranch = session.SenseBranch(context.Background(), cwd)
			}
			if sessionObjective == "" {
				sessionObjective = "Task in progress"
			}

			manifest := session.NewSessionManifest(sessionID, cwd, sessionBranch, sessionObjective)

			// Populate from CLI flags
			manifest.Decisions = sessionDecisions
			manifest.Constraints = sessionConstraints
			manifest.PendingTasks = sessionPending

			// Auto-sense changed files from git
			gitSnapshots, err := session.SenseChangedFiles(context.Background(), cwd)
			if err == nil && len(gitSnapshots) > 0 {
				manifest.ChangedFiles = append(manifest.ChangedFiles, gitSnapshots...)
			}

			// Add manually specified files (for non-git workspaces)
			for _, f := range sessionFiles {
				hash, hashErr := session.StreamSHA256(filepath.Join(cwd, f))
				if hashErr != nil {
					continue
				}
				manifest.ChangedFiles = append(manifest.ChangedFiles, session.FileSnapshot{
					Path: f,
					Hash: hash,
				})
			}

			// Open store BEFORE serializing to splice artifact IDs
			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			// Splice recent unexpired artifact IDs
			artifactIDs, err := s.GetRecentUnexpiredArtifactIDs(cwd, 20)
			if err == nil && len(artifactIDs) > 0 {
				manifest.ArtifactIDs = artifactIDs
			}

			// Now serialize with all data populated
			manifestJSON, err := manifest.ToJSON()
			if err != nil {
				return err
			}

			if err := s.PutSession(sessionID, cwd, sessionBranch, manifest.SchemaVersion, manifestJSON); err != nil {
				return fmt.Errorf("failed to save session: %w", err)
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("✓ Session %s saved to local store", sessionID)))
			fmt.Println(manifest.FormatMarkdown())

			if sessionExportFile != "" {
				if err := os.WriteFile(sessionExportFile, []byte(manifestJSON), 0644); err != nil {
					return fmt.Errorf("failed to export session file: %w", err)
				}
				fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Manifest exported to %s", sessionExportFile)))
			}

			return nil
		},
	}
	sessionSaveCmd.Flags().StringVarP(&sessionObjective, "objective", "o", "", "Task objective")
	sessionSaveCmd.Flags().StringVar(&sessionBranch, "branch", "", "Active branch name (auto-sensed from git if omitted)")
	sessionSaveCmd.Flags().StringVarP(&sessionExportFile, "export", "e", "", "File path to export JSON manifest")
	sessionSaveCmd.Flags().StringArrayVarP(&sessionDecisions, "decision", "d", nil, "Architectural decision (repeatable)")
	sessionSaveCmd.Flags().StringArrayVarP(&sessionConstraints, "constraint", "c", nil, "Approved constraint (repeatable)")
	sessionSaveCmd.Flags().StringArrayVarP(&sessionPending, "pending", "p", nil, "Outstanding task (repeatable)")
	sessionSaveCmd.Flags().StringArrayVarP(&sessionFiles, "file", "F", nil, "Manual file to snapshot (repeatable, for non-git workspaces)")

	sessionExportCmd := &cobra.Command{
		Use:   "export [session-id]",
		Short: "Export stored session manifest to JSON or Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			cwd, _ := os.Getwd()

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			manifestJSON, err := s.GetSession(sessionID, cwd)
			if err != nil {
				return err
			}

			sm, err := session.FromJSON(manifestJSON)
			if err != nil {
				return err
			}

			fmt.Println(sm.FormatMarkdown())
			return nil
		},
	}

	var sessionForceLoad bool
	sessionLoadCmd := &cobra.Command{
		Use:   "load [file-or-session-id]",
		Short: "Load a session manifest with freshness validation and workspace isolation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			cwd, _ := os.Getwd()

			var manifestJSON string
			if _, err := os.Stat(target); err == nil {
				data, err := os.ReadFile(target)
				if err != nil {
					return err
				}
				manifestJSON = string(data)
			} else {
				s, err := store.OpenStore(getDBPath())
				if err != nil {
					return fmt.Errorf("failed to open database: %w", err)
				}
				defer s.Close()
				m, err := s.GetSession(target, cwd)
				if err != nil {
					return err
				}
				manifestJSON = m
			}

			sm, err := session.ImportSession(manifestJSON, cwd)
			if err != nil {
				return err
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("📋 Tzro Session Handoff: %s", sm.ID)))
			fmt.Printf("Workspace: %s | Branch: %s\n\n", sm.Workspace, sm.Branch)

			// Freshness check
			drifts := sm.ValidateFreshness(cwd)
			hasModified := false
			for _, d := range drifts {
				switch d.Status {
				case "modified":
					hasModified = true
					fmt.Printf("  %s %s: content modified since session was saved\n", warnStyle.Render("DRIFT"), d.Path)
				case "missing":
					hasModified = true
					fmt.Printf("  %s %s: file missing from disk\n", warnStyle.Render("MISSING"), d.Path)
				case "fresh":
					fmt.Printf("  %s %s (intact)\n", infoStyle.Render("VERIFIED"), d.Path)
				}
			}

			if hasModified && !sessionForceLoad {
				fmt.Println(warnStyle.Render("\nWarning: Workspace file drift detected relative to saved session."))
			}

			// Check freshness
			_, checkReports := sm.ValidateCheckFreshness(cwd)
			for _, cr := range checkReports {
				if !cr.Fresh {
					fmt.Printf("  %s Check %q has drifted files: %s\n", warnStyle.Render("STALE CHECK"), cr.Check.Command, strings.Join(cr.DriftedFiles, ", "))
				}
			}

			// Missing artifacts
			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
				missingArts := sm.CheckMissingArtifacts(s)
				for _, a := range missingArts {
					fmt.Printf("  %s Artifact %s not found in store\n", warnStyle.Render("MISSING ARTIFACT"), a)
				}
			}

			fmt.Println(infoStyle.Render("\n✔ Session loaded successfully."))
			fmt.Println(sm.FormatMarkdown())
			return nil
		},
	}
	sessionLoadCmd.Flags().BoolVarP(&sessionForceLoad, "force", "f", false, "Force loading session even if workspace files have drifted")

	// COMMIT COMMAND (Explicit Host Agent Intent Capture)
	var commitObjective string
	var commitDecisions []string
	var commitConstraints []string
	var commitPending []string
	sessionCommitCmd := &cobra.Command{
		Use:   "commit",
		Short: "Explicitly commit agent intent (objective, decisions, constraints, pending tasks)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			branch := session.SenseBranch(context.Background(), cwd)

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			sm, _ := session.ResolveSession(s, cwd, branch, "")
			if sm == nil {
				sessionID := fmt.Sprintf("sess_%d", time.Now().Unix())
				sm = session.NewSessionManifest(sessionID, cwd, branch, commitObjective)
			}

			if commitObjective != "" {
				sm.Objective = commitObjective
			}
			sm.Decisions = append(sm.Decisions, commitDecisions...)
			sm.Constraints = append(sm.Constraints, commitConstraints...)
			sm.PendingTasks = append(sm.PendingTasks, commitPending...)

			// Auto-sense changed files
			gitSnapshots, err := session.SenseChangedFiles(context.Background(), cwd)
			if err == nil && len(gitSnapshots) > 0 {
				sm.ChangedFiles = gitSnapshots
			}

			manifestJSON, err := sm.ToJSON()
			if err != nil {
				return err
			}

			if err := s.PutSession(sm.ID, cwd, sm.Branch, sm.SchemaVersion, manifestJSON); err != nil {
				return fmt.Errorf("failed to commit session: %w", err)
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("✓ Committed intent to session %s", sm.ID)))
			fmt.Println(sm.FormatMarkdown())
			return nil
		},
	}
	sessionCommitCmd.Flags().StringVarP(&commitObjective, "objective", "o", "", "Task objective")
	sessionCommitCmd.Flags().StringArrayVarP(&commitDecisions, "decision", "d", nil, "Architectural decision (repeatable)")
	sessionCommitCmd.Flags().StringArrayVarP(&commitConstraints, "constraint", "c", nil, "Approved constraint (repeatable)")
	sessionCommitCmd.Flags().StringArrayVarP(&commitPending, "pending", "p", nil, "Outstanding task (repeatable)")

	// STATUS COMMAND
	sessionStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect the status and freshness of the active session",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			branch := session.SenseBranch(context.Background(), cwd)

			s, err := store.OpenStore(getDBPath())
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			sm, err := session.ResolveSession(s, cwd, branch, "")
			if err != nil {
				return err
			}
			if sm == nil {
				fmt.Println(infoStyle.Render("No active session found for current workspace."))
				return nil
			}

			fmt.Println(titleStyle.Render(fmt.Sprintf("📋 Active Session: %s", sm.ID)))
			fmt.Printf("Workspace: %s | Branch: %s\n\n", sm.Workspace, sm.Branch)

			drifts, checkReports := sm.ValidateCheckFreshness(cwd)
			for _, d := range drifts {
				switch d.Status {
				case "modified":
					fmt.Printf("  %s %s: modified on disk\n", warnStyle.Render("DRIFT"), d.Path)
				case "missing":
					fmt.Printf("  %s %s: missing from disk\n", warnStyle.Render("MISSING"), d.Path)
				case "fresh":
					fmt.Printf("  %s %s\n", infoStyle.Render("FRESH"), d.Path)
				}
			}

			for _, cr := range checkReports {
				if !cr.Fresh {
					fmt.Printf("  %s Check %q stale due to: %s\n", warnStyle.Render("STALE"), cr.Check.Command, strings.Join(cr.DriftedFiles, ", "))
				} else {
					fmt.Printf("  %s Check %q verified fresh\n", infoStyle.Render("FRESH"), cr.Check.Command)
				}
			}

			missingArts := sm.CheckMissingArtifacts(s)
			for _, a := range missingArts {
				fmt.Printf("  %s Artifact %s absent from store\n", warnStyle.Render("MISSING"), a)
			}

			return nil
		},
	}

	sessionCmd.AddCommand(sessionCommitCmd, sessionSaveCmd, sessionExportCmd, sessionLoadCmd, sessionStatusCmd)

	// ARTIFACTS COMMAND GROUP
	artifactsCmd := &cobra.Command{
		Use:   "artifacts",
		Short: "Manage stored artifacts (list, prune, inspect quotas)",
	}

	var artifactsListWorkspace string
	var artifactsListJSON bool
	artifactsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List stored artifacts with size, type, pinned status, and expiration",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := getDBPath()
			s, err := store.OpenStore(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			ws := artifactsListWorkspace
			if ws == "" {
				ws, _ = os.Getwd()
			}

			artifacts, err := s.ListArtifacts(ws)
			if err != nil {
				return err
			}

			if artifactsListJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(artifacts)
			}

			if len(artifacts) == 0 {
				fmt.Println(infoStyle.Render("No artifacts found for workspace: " + ws))
				return nil
			}

			fmt.Println(titleStyle.Render("📦 Artifacts"))
			fmt.Printf("Workspace: %s\n\n", ws)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tSIZE\tPINNED\tEXPIRES\tCREATED")
			for _, a := range artifacts {
				sizeStr := formatBytes(a.SizeBytes)
				pinnedStr := "no"
				if a.Pinned {
					pinnedStr = "yes"
				}
				expiresStr := a.ExpiresAt.Format(time.RFC3339)
				if a.ExpiresAt.Before(time.Now()) && !a.Pinned {
					expiresStr = warnStyle.Render("EXPIRED")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					a.ID, a.Type, sizeStr, pinnedStr, expiresStr, a.CreatedAt.Format(time.RFC3339))
			}
			w.Flush()

			count, totalBytes, _ := s.GetWorkspaceArtifactStats(ws)
			fmt.Printf("\nTotal: %d artifacts, %s\n", count, formatBytes(totalBytes))
			return nil
		},
	}
	artifactsListCmd.Flags().StringVar(&artifactsListWorkspace, "workspace", "", "Workspace path (defaults to cwd)")
	artifactsListCmd.Flags().BoolVar(&artifactsListJSON, "json", false, "Output as JSON")

	var artifactsPruneWorkspace string
	var artifactsPruneExpiredOnly bool
	var artifactsPruneMaxMB int64
	var artifactsPruneMaxCount int
	artifactsPruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune artifacts by expiration, size, or count limits",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := getDBPath()
			s, err := store.OpenStore(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer s.Close()

			ws := artifactsPruneWorkspace
			if ws == "" {
				ws, _ = os.Getwd()
			}

			var totalDeleted int64

			// Always sweep expired first
			expired, err := s.EvictExpiredArtifacts()
			if err != nil {
				return fmt.Errorf("expiry sweep failed: %w", err)
			}
			totalDeleted += expired
			if expired > 0 {
				fmt.Printf("  Swept %d expired artifacts\n", expired)
			}

			if !artifactsPruneExpiredOnly {
				maxBytes := store.DefaultWorkspaceMaxBytes
				if artifactsPruneMaxMB > 0 {
					maxBytes = artifactsPruneMaxMB * 1024 * 1024
				}
				maxCount := store.DefaultWorkspaceMaxCount
				if artifactsPruneMaxCount > 0 {
					maxCount = artifactsPruneMaxCount
				}

				evicted, err := s.EvictArtifactsLRU(ws, maxCount, maxBytes)
				if err != nil {
					return fmt.Errorf("LRU eviction failed: %w", err)
				}
				totalDeleted += evicted
				if evicted > 0 {
					fmt.Printf("  Evicted %d artifacts via LRU (max-count=%d, max-mb=%d)\n", evicted, maxCount, maxBytes/(1024*1024))
				}
			}

			if totalDeleted == 0 {
				fmt.Println(infoStyle.Render("✓ Nothing to prune — workspace is within quota."))
			} else {
				fmt.Println(infoStyle.Render(fmt.Sprintf("✓ Pruned %d total artifacts.", totalDeleted)))
			}

			count, totalBytes, _ := s.GetWorkspaceArtifactStats(ws)
			fmt.Printf("  Remaining: %d artifacts, %s\n", count, formatBytes(totalBytes))
			return nil
		},
	}
	artifactsPruneCmd.Flags().StringVar(&artifactsPruneWorkspace, "workspace", "", "Workspace path (defaults to cwd)")
	artifactsPruneCmd.Flags().BoolVar(&artifactsPruneExpiredOnly, "expired-only", false, "Only prune expired artifacts, skip LRU eviction")
	artifactsPruneCmd.Flags().Int64Var(&artifactsPruneMaxMB, "max-mb", 0, "Maximum workspace size in MB (default: 100)")
	artifactsPruneCmd.Flags().IntVar(&artifactsPruneMaxCount, "max-count", 0, "Maximum artifact count (default: 1000)")

	artifactsCmd.AddCommand(artifactsListCmd, artifactsPruneCmd)

	// BENCH COMMAND
	benchCmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark suite measuring signal density, latency, and cache efficiency",
	}

	var benchModel string
	var benchTier string
	var benchPrimitive string
	var benchMaxCost float64
	var benchTimeout time.Duration
	var benchOutput string
	var benchNoCache bool
	var benchSamples int
	var benchBaseURL string
	var benchAPIKey string

	signalDensityCmd := &cobra.Command{
		Use:          "signal-density",
		Short:        "Measure task-completion signal density per token across Tzro optimizations",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := signaldensity.BenchmarkConfig{
				Model:      benchModel,
				Tier:       benchTier,
				Primitive:  benchPrimitive,
				MaxCost:    benchMaxCost,
				Timeout:    benchTimeout,
				OutputPath: benchOutput,
				NoCache:    benchNoCache,
				Samples:    benchSamples,
				BaseURL:    benchBaseURL,
				APIKey:     benchAPIKey,
				StorePath:  getDBPath(),
			}

			runner := signaldensity.NewRunner(cfg)
			report, err := runner.Run(cmd.Context())
			if err != nil {
				return err
			}

			table := signaldensity.RenderTerminalReport(report, benchMaxCost)
			fmt.Println(table)

			savedPath, err := signaldensity.SaveJSONReport(report, benchOutput)
			if err != nil {
				return fmt.Errorf("failed to save JSON report: %w", err)
			}
			fmt.Printf("✓ Benchmark report saved to: %s\n\n", savedPath)
			return nil
		},
	}

	signalDensityCmd.Flags().StringVar(&benchModel, "model", "anthropic/claude-3.5-sonnet", "Target model (OpenRouter or direct provider ID)")
	signalDensityCmd.Flags().StringVar(&benchTier, "tier", "all", "Filter by tier: all, micro, macro")
	signalDensityCmd.Flags().StringVar(&benchPrimitive, "primitive", "all", "Filter micro primitive: all, skeleton, compactor, json, tabular")
	signalDensityCmd.Flags().Float64Var(&benchMaxCost, "max-cost", 2.00, "Hard spending limit in USD; halts immediately if exceeded")
	signalDensityCmd.Flags().DurationVar(&benchTimeout, "timeout", 180*time.Second, "Per-request timeout")
	signalDensityCmd.Flags().StringVar(&benchOutput, "output", "", "Optional filepath to persist complete JSON execution trace")
	signalDensityCmd.Flags().BoolVar(&benchNoCache, "no-cache", true, "Busts KV cache headers to measure raw input tokens cleanly")
	signalDensityCmd.Flags().IntVar(&benchSamples, "samples", 1, "Number of evaluation samples per test case")
	signalDensityCmd.Flags().StringVar(&benchBaseURL, "base-url", "", "Custom base URL for LLM API (defaults to OpenRouter)")
	signalDensityCmd.Flags().StringVar(&benchAPIKey, "api-key", "", "API key for LLM provider (defaults to env vars)")

	benchCmd.AddCommand(signalDensityCmd)

	rootCmd.AddCommand(startCmd, probeCmd, skeletonCmd, expandCmd, compactCmd, hookCmd, initCmd, statusCmd, doctorCmd, queryCmd, ingestCmd, dlpCmd, contextCmd, impactCmd, searchCmd, inspectCmd, sessionCmd, artifactsCmd, benchCmd)

	return rootCmd
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}


