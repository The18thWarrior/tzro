package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// Shared test fixtures
// --------------------------------------------------------------------------

// grepCommandPayloads returns harness-specific JSON payloads for a grep command.
func grepCommandPayloads() map[string]string {
	return map[string]string{
		"Antigravity": `{"toolCall":{"name":"run_command","args":{"CommandLine":"grep -rn 'func main' ."}},"stepIdx":1,"conversationId":"bench-001"}`,
		"Claude":      `{"tool_name":"Bash","tool_input":{"command":"grep -rn 'func main' ."}}`,
		"Hermes":      `{"tool":"execute_command","parameters":{"command":"grep -rn 'func main' ."}}`,
		"Copilot":     `{"tool":"runInTerminal","input":{"command":"grep -rn 'func main' ."}}`,
		"PiCoder":     `{"tool_name":"bash","tool_input":{"command":"grep -rn 'func main' ."}}`,
	}
}

// verboseLogPayload returns a ~5KB synthetic stack trace for post-tool compaction benchmarks.
// Frame lines must match the compactor's regex (lines starting with runtime/, testing.go, etc.).
func verboseLogPayload() string {
	var sb strings.Builder
	sb.WriteString("panic: runtime error: index out of range [42] with length 10\n\n")
	sb.WriteString("goroutine 1 [running]:\n")
	sb.WriteString("main.processData(0xc0000b2000, 0xa, 0x2a)\n")
	sb.WriteString("\t/app/server/handler.go:142 +0x1a5\n")
	sb.WriteString("main.handleRequest(0xc0000fe000)\n")
	sb.WriteString("\t/app/server/router.go:87 +0x312\n")
	// Generate ~100 runtime/framework frames that match the compactor's goRuntimeFrameRe
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 200+i, 0x10+i))
	}
	sb.WriteString("\nexit status 2\n")
	return sb.String()
}

// postToolPayloads returns harness-specific JSON for post-tool with a verbose log body.
func postToolPayloads(logBody string) map[string]string {
	escaped, _ := json.Marshal(logBody)
	return map[string]string{
		"Antigravity": `{"toolCall":{"name":"run_command"},"output":` + string(escaped) + `}`,
		"Claude":      `{"tool_name":"Bash","tool_output":` + string(escaped) + `}`,
		"Hermes":      `{"tool":"execute_command","output":` + string(escaped) + `}`,
		"Copilot":     `{"tool":"runInTerminal","output":` + string(escaped) + `}`,
		"PiCoder":     `{"tool_name":"bash","tool_output":` + string(escaped) + `}`,
	}
}

// --------------------------------------------------------------------------
// Pre-tool handler dispatch
// --------------------------------------------------------------------------

func runPreTool(harness string, payload string, out *bytes.Buffer) error {
	r := strings.NewReader(payload)
	switch harness {
	case "Antigravity":
		return HandlePreToolUse(r, out, nil)
	case "Claude":
		return HandleClaudePreToolUse(r, out, nil)
	case "Hermes":
		return HandleHermesPreTool(r, out, nil)
	case "Copilot":
		return HandleCopilotPreTool(r, out, nil)
	case "PiCoder":
		return HandlePiCoderPreTool(r, out, nil)
	default:
		return fmt.Errorf("unknown harness: %s", harness)
	}
}

func runPostTool(harness string, payload string, out *bytes.Buffer) error {
	r := strings.NewReader(payload)
	switch harness {
	case "Antigravity":
		return HandlePostToolUse(r, out, nil)
	case "Claude":
		return HandleClaudePostToolUse(r, out, nil)
	case "Hermes":
		return HandleHermesPostTool(r, out, nil)
	case "Copilot":
		return HandleCopilotPostTool(r, out, nil)
	case "PiCoder":
		return HandlePiCoderPostTool(r, out, nil)
	default:
		return fmt.Errorf("unknown harness: %s", harness)
	}
}

// --------------------------------------------------------------------------
// 1. Latency / Throughput Benchmarks
// --------------------------------------------------------------------------

func BenchmarkPreTool(b *testing.B) {
	payloads := grepCommandPayloads()
	for harness, payload := range payloads {
		b.Run(harness, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out bytes.Buffer
				if err := runPreTool(harness, payload, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPostTool(b *testing.B) {
	logBody := verboseLogPayload()
	payloads := postToolPayloads(logBody)
	for harness, payload := range payloads {
		b.Run(harness, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out bytes.Buffer
				if err := runPostTool(harness, payload, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// 2. Token Savings Benchmarks
// --------------------------------------------------------------------------

func TestTokenSavings(t *testing.T) {
	logBody := verboseLogPayload()
	payloads := postToolPayloads(logBody)
	bytesIn := len(logBody)

	for harness, payload := range payloads {
		t.Run(harness, func(t *testing.T) {
			var out bytes.Buffer
			if err := runPostTool(harness, payload, &out); err != nil {
				t.Fatalf("post-tool failed: %v", err)
			}

			// Extract the actual output content from the JSON response.
			// Antigravity returns "{}\n" (pass-through), so measure the raw response.
			var bytesOut int
			if harness == "Antigravity" {
				bytesOut = out.Len()
			} else {
				var resp map[string]any
				if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse response: %v", err)
				}
				// Each harness uses a different key for the output field
				for _, key := range []string{"tool_output", "output"} {
					if v, ok := resp[key]; ok {
						if s, ok := v.(string); ok {
							bytesOut = len(s)
							break
						}
					}
				}
				if bytesOut == 0 {
					bytesOut = out.Len()
				}
			}

			ratio := 1.0 - float64(bytesOut)/float64(bytesIn)

			t.Logf("harness=%-14s bytes_in=%d  bytes_out=%d  savings_ratio=%.1f%%",
				harness, bytesIn, bytesOut, ratio*100)

			// All harnesses that compact should achieve meaningful savings.
			// Antigravity post-tool is a pass-through, so skip threshold check.
			if harness != "Antigravity" && ratio < 0.40 {
				t.Errorf("expected >=40%% savings for %s, got %.1f%%", harness, ratio*100)
			}
		})
	}
}
