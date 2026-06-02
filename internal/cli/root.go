package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
	"tzro/internal/config"
	"tzro/examples/tui"

	"github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

// GlobalFlags holds parsed persistent CLI flags.
type GlobalFlags struct {
	URL     string
	Offline bool
	DBPath  string
	JSONOut bool
}

var globalFlags GlobalFlags

// RootCmd is the baseline command line handler.
var RootCmd = &cobra.Command{
	Use:   "tzro",
	Short: "tzro - Command Line Interface client for the durable agentic engine",
	Long:  `tzro is a local-first durable agentic execution engine CLI and TUI developer tool.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return config.Load()
	},
	Run: func(cmd *cobra.Command, args []string) {
		// Get resolved client (connected REST or read-only Direct DB)
		c, err := GetClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing client: %v\n", err)
			os.Exit(1)
		}

		// Launch Bubble Tea TUI Program in fullscreen alt screen buffer
		model := tui.NewModel(c)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "TUI runtime error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	initRootFlags(RootCmd)
}

func initRootFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&globalFlags.URL, "url", "http://localhost:8080", "tzro server daemon REST URL connection endpoint")
	cmd.PersistentFlags().BoolVar(&globalFlags.Offline, "offline", false, "Force direct local SQLite database inspection mode (read-only)")
	cmd.PersistentFlags().StringVar(&globalFlags.DBPath, "db", "tzro.db", "Path to local SQLite graph database file")
	cmd.PersistentFlags().BoolVarP(&globalFlags.JSONOut, "json", "j", false, "Output raw, minified JSON payload instead of styled tabular text")
}

// GetClient resolves connected RESTClient or read-only DirectDBClient based on flags and daemon status.
func GetClient() (tui.TZROClient, error) {
	if globalFlags.Offline {
		return NewDirectDBClient(globalFlags.DBPath), nil
	}

	// 100ms quick HTTP ping check to see if server is online
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
	}
	resp, err := client.Get(globalFlags.URL + "/api/config")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return NewRESTClient(globalFlags.URL), nil
		}
	}

	// Server is unreachable, print fallback warning to stderr (unless raw json output is requested)
	if !globalFlags.JSONOut {
		fmt.Fprintf(os.Stderr, "[Connected Warning] Cannot reach server daemon on %s. Falling back to offline database inspection mode.\n", globalFlags.URL)
	}

	return NewDirectDBClient(globalFlags.DBPath), nil
}

// printJSON marshals data into raw minified JSON formatting and writes it.
func printJSON(w io.Writer, data any) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.Write(bytes)
	return err
}
