package signaldensity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pretty names for the batteries.
var batteryDisplayNames = map[string]string{
	BatteryASTSkeleton:   "AST Skeletonization",
	BatteryCompactorLogs: "Log Compactor",
	BatterySmartJSON:     "Smart JSON Crusher",
	BatteryTabularSQL:    "Tabular SQL Ingestion",
	BatteryMiniMacro:     "Mini-Macro Coding",
}

func getDisplayName(batteryName string) string {
	if name, ok := batteryDisplayNames[batteryName]; ok {
		return name
	}
	// Fallback: capitalize words
	parts := strings.Split(batteryName, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// formatIntWithCommas formats an integer with thousands separator.
func formatIntWithCommas(n int) string {
	in := fmt.Sprintf("%d", n)
	if len(in) <= 3 {
		return in
	}
	var out []byte
	rem := len(in) % 3
	if rem > 0 {
		out = append(out, in[:rem]...)
		if len(in) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}

// RenderTerminalReport formats the benchmark report as a clean terminal table.
func RenderTerminalReport(report *BenchmarkReport, maxCost float64) string {
	var sb strings.Builder

	border := strings.Repeat("=", 88)
	divider := strings.Repeat("─", 88)

	sb.WriteString(border + "\n")
	sb.WriteString(fmt.Sprintf("%*s\n", (88+36)/2, "TZRO SIGNAL DENSITY BENCHMARK REPORT"))
	sb.WriteString(border + "\n")

	sb.WriteString(fmt.Sprintf("Model: %s | Max Cost: $%.2f | Total Spent: $%.4f\n",
		report.Metadata.Model, maxCost, report.Metadata.TotalCostUSD))
	sb.WriteString(divider + "\n")

	// Header: 24, 10, 10, 10, 10, 10, 11, 7
	sb.WriteString(fmt.Sprintf("%-24s %10s %10s %9s %9s %10s %11s %7s\n",
		"Component / Battery", "Raw Tok", "Tzro Tok", "Acc (R)", "Acc (T)", "Ret (SR)", "Comp (CR)", "SDM"))
	sb.WriteString(divider + "\n")

	for _, b := range report.Batteries {
		dispName := getDisplayName(b.Name)
		sb.WriteString(fmt.Sprintf("%-24s %10s %10s %8.1f%% %8.1f%% %9.2fx %10.2fx %6.2fx\n",
			dispName,
			formatIntWithCommas(b.RawTokens),
			formatIntWithCommas(b.TzroTokens),
			b.AccuracyRaw*100.0,
			b.AccuracyTzro*100.0,
			b.SignalRetention,
			b.CompressionRatio,
			b.SDM,
		))
	}

	sb.WriteString(divider + "\n")
	sb.WriteString(fmt.Sprintf("%-24s %10s %10s %8.1f%% %8.1f%% %9.2fx %10.2fx %6.2fx\n",
		"COMPOSITE SCORE",
		formatIntWithCommas(report.Summary.RawTokens),
		formatIntWithCommas(report.Summary.TzroTokens),
		report.Summary.OverallAccuracyRaw*100.0,
		report.Summary.OverallAccuracyTzro*100.0,
		report.Summary.SignalRetention,
		report.Summary.CompressionRatio,
		report.Summary.CompositeSDM,
	))
	sb.WriteString(border + "\n")

	return sb.String()
}

// SaveJSONReport saves the report to the destination path.
func SaveJSONReport(report *BenchmarkReport, outputPath string) (string, error) {
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		cleanModel := strings.ReplaceAll(strings.ReplaceAll(report.Metadata.Model, "/", "_"), ":", "_")
		ts := time.Now().UTC().Format("20060102_150405")
		outputPath = filepath.Join(home, ".tzro", "benchmarks", fmt.Sprintf("sdm_%s_%s.json", cleanModel, ts))
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for report: %w", err)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal benchmark report: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write benchmark report to %s: %w", outputPath, err)
	}

	return outputPath, nil
}
