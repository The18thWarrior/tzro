package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var restartReason string

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the tzro daemon (tzrod) in-place",
	Long:  `Triggers an immediate in-place re-exec of the running tzrod daemon. The daemon replaces itself with a fresh copy of the same binary, preserving the PID and pidlock. In-flight tasks are interrupted and recovered automatically on boot. The inference sidecar survives via process adoption.`,
	Run: func(cmd *cobra.Command, args []string) {
		if globalFlags.Offline {
			fmt.Fprintln(os.Stderr, "Error: restart is not available in offline mode. The daemon must be running.")
			os.Exit(1)
		}

		daemonURL := getDaemonURL()

		// Build request body
		body := map[string]string{}
		if restartReason != "" {
			body["reason"] = restartReason
		}
		bodyBytes, _ := json.Marshal(body)

		// POST to daemon
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(daemonURL+"/api/restart", "application/json", bytes.NewReader(bodyBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not reach daemon at %s: %v\n", daemonURL, err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		respBytes, _ := io.ReadAll(resp.Body)
		var restartResp struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
			Uptime string `json:"uptime"`
		}
		_ = json.Unmarshal(respBytes, &restartResp)

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error: daemon returned status %d: %s\n", resp.StatusCode, string(respBytes))
			os.Exit(1)
		}

		fmt.Printf("Restart initiated (reason: %s, previous uptime: %s)\n", restartResp.Reason, restartResp.Uptime)
		fmt.Println("Verifying daemon is back up...")

		// Fire-and-verify: wait for the daemon to come back
		time.Sleep(300 * time.Millisecond)

		healthClient := &http.Client{Timeout: 2 * time.Second}
		verified := false

		for attempt := range 5 {
			healthResp, err := healthClient.Get(daemonURL + "/health")
			if err == nil && healthResp.StatusCode == http.StatusOK {
				healthResp.Body.Close()
				verified = true
				break
			}
			if attempt < 4 {
				time.Sleep(500 * time.Millisecond)
			}
		}

		if !verified {
			fmt.Fprintln(os.Stderr, "Warning: daemon did not respond after restart. Check daemon logs.")
			os.Exit(1)
		}

		fmt.Println("✓ Daemon restarted successfully.")
	},
}

func init() {
	restartCmd.Flags().StringVar(&restartReason, "reason", "", "Reason for the restart (logged for audit)")
	RootCmd.AddCommand(restartCmd)
}
