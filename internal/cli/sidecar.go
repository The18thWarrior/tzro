package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

var sidecarCmd = &cobra.Command{
	Use:   "sidecar",
	Short: "Monitor and manage the local llama-server GGUF execution sidecar",
}

var sidecarStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show llama-server sidecar PID, active port, and manifest warming progress",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		status, err := client.GetSidecarStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, status)
			return
		}

		headers := []string{"METRIC", "VALUE"}
		rows := [][]string{
			{"Active Status", status.Status},
			{"Listening Port", strconv.Itoa(status.ActivePort)},
			{"System PID", strconv.Itoa(status.ActivePID)},
			{"Model Path", status.GGUFModelPath},
			{"KV Cache progress", fmt.Sprintf("%d%%", status.ManifestProgress)},
		}

		printTable(headers, rows)
	},
}

var sidecarActionCmd = &cobra.Command{
	Use:   "control [start|stop|gc]",
	Short: "Control local model sidecar startup, shutdown, or active slot GC garbage collection",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		action := args[0]
		if action != "start" && action != "stop" && action != "gc" {
			fmt.Fprintln(os.Stderr, "Error: action must be start, stop, or gc")
			os.Exit(1)
		}

		if globalFlags.Offline {
			fmt.Fprintln(os.Stderr, "Error: sidecar control requires an active daemon connection (Connected mode only)")
			os.Exit(1)
		}

		serverAction := action
		if action == "gc" {
			serverAction = "erase_cache"
		}

		reqBody, _ := json.Marshal(map[string]string{"action": serverAction})
		resp, err := http.Post(globalFlags.URL+"/api/sidecar", "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: connection failed: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error: action failed with status %s\n", resp.Status)
			os.Exit(1)
		}

		fmt.Printf("✔ Local model sidecar action '%s' triggered successfully on daemon!\n", action)
	},
}

func init() {
	sidecarCmd.AddCommand(sidecarStatusCmd)
	sidecarCmd.AddCommand(sidecarActionCmd)
	RootCmd.AddCommand(sidecarCmd)
}
