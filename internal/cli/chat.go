package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"tzro/internal/server"
	"tzro/internal/stream"

	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "Submit a natural-language prompt to the classification and execution engine",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		message := strings.Join(args, " ")

		if globalFlags.Offline {
			fmt.Fprintln(os.Stderr, "Error: chat console requires a running server daemon (Connected mode only)")
			os.Exit(1)
		}

		urlStr := getDaemonURL()
		reqBody, _ := json.Marshal(server.ChatRequest{Message: message})
		resp, err := http.Post(urlStr+"/api/chat", "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: connection failed: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error: chat failed with status %s\n", resp.Status)
			os.Exit(1)
		}

		var chatResp server.ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to decode response: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, chatResp)
			return
		}

		// Human-friendly console printout
		fmt.Printf("[Classification] Intent: %s (Confidence: %.2f)\n", chatResp.Intent.Type, chatResp.Intent.Confidence)
		fmt.Printf("[Classification] Complexity: %s\n", chatResp.Complexity)
		fmt.Printf("[Classification] Summary: %s\n\n", chatResp.Message)

		if chatResp.Graph != nil {
			fmt.Println("========================================================================")
			fmt.Printf("           COMPILED TASK GRAPH DAG (Task ID: %s)\n", chatResp.TaskID)
			fmt.Println("========================================================================")
			fmt.Printf("MaxCycles: %d | Parallel levels sorted by Kahn compiler:\n", chatResp.Graph.MaxCycles)
			for i, level := range chatResp.Levels {
				fmt.Printf("  Layer %d: [%s]\n", i+1, strings.Join(level, "], ["))
			}
			fmt.Println("========================================================================")
			fmt.Println("✔ Task execution started successfully in the background daemon.")
			fmt.Printf("Monitor status using: tzro task status %s\n", chatResp.TaskID)
		} else if chatResp.StreamID != "" {
			fmt.Println("--- LLM CONVERSATIONAL RESPONSE ---")

			// Establish SSE connection to tail chunks live
			eventsResp, err := http.Get(urlStr + "/api/events")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to connect to live stream: %v\n", err)
				return
			}
			defer eventsResp.Body.Close()

			scanner := bufio.NewScanner(eventsResp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				jsonStr := strings.TrimPrefix(line, "data: ")
				var chunk stream.StreamChunk
				if err := json.Unmarshal([]byte(jsonStr), &chunk); err == nil {
					if chunk.StreamID == chatResp.StreamID {
						fmt.Print(chunk.Content)
					}
					// Check if stream finished
					if chunk.StreamID == chatResp.StreamID && chunk.Type == "done" {
						break
					}
				}
			}
			fmt.Println("\n-----------------------------------")
		}
	},
}

func init() {
	RootCmd.AddCommand(chatCmd)
}
