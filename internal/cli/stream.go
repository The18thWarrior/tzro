package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"tzro/internal/stream"

	"github.com/spf13/cobra"
)

var streamCmd = &cobra.Command{
	Use:   "stream",
	Short: "Tail the raw server StreamBus event channel live (Connected mode only)",
	Run: func(cmd *cobra.Command, args []string) {
		if globalFlags.Offline {
			fmt.Fprintln(os.Stderr, "Error: stream tailing requires an active daemon connection")
			os.Exit(1)
		}

		urlStr := getDaemonURL()
		resp, err := http.Get(urlStr + "/api/events")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to connect to stream: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error: server returned status %s\n", resp.Status)
			os.Exit(1)
		}

		reader := bufio.NewReader(resp.Body)
		fmt.Println(" Tailing StreamBus events... Press Ctrl+C to exit.")
		fmt.Println("========================================================================")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Stream disconnected: %v\n", err)
				return
			}

			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			jsonStr := strings.TrimPrefix(line, "data: ")
			var chunk stream.StreamChunk
			if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
				continue
			}

			if globalFlags.JSONOut {
				fmt.Println(jsonStr)
				continue
			}

			// Styled, readable output
			prefix := fmt.Sprintf("[%s]", strings.ToUpper(chunk.Source))
			if chunk.Type != "" {
				prefix += fmt.Sprintf(" (%s)", chunk.Type)
			}
			fmt.Printf("%-22s | %s\n", prefix, chunk.Content)
		}
	},
}

func init() {
	RootCmd.AddCommand(streamCmd)
}
