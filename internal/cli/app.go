package cli

import (
	"database/sql"
	"fmt"
	"os"
	"time"
	"tzro/internal/config"
	"tzro/internal/mcp"
	"tzro/internal/packagemanager"

	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage installed Agent Apps (.tzroapp packages)",
}

var appInstallCmd = &cobra.Command{
	Use:   "install [path-to-tzroapp]",
	Short: "Install an Agent App from a .tzroapp archive",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		archivePath := args[0]

		mgr, cleanup, err := getPackageManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()

		app, err := mgr.Install(archivePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, app)
			return
		}

		fmt.Printf("✓ Installed Agent App '%s' (v%s)\n", app.Name, app.Version)
		fmt.Printf("  ID:           %s\n", app.ID)
		fmt.Printf("  Status:       %s\n", app.Status)
		if len(app.Capabilities) > 0 {
			fmt.Printf("  Capabilities: %v\n", app.Capabilities)
		}
	},
}

var appUninstallCmd = &cobra.Command{
	Use:   "uninstall [app-id]",
	Short: "Soft-disable an Agent App (preserves data and files)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appID := args[0]

		mgr, cleanup, err := getPackageManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()

		if err := mgr.Uninstall(appID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if !globalFlags.JSONOut {
			fmt.Printf("✓ Uninstalled Agent App '%s' (data preserved)\n", appID)
			fmt.Println("  Use 'tzro app purge' to permanently remove all data.")
		}
	},
}

var appPurgeCmd = &cobra.Command{
	Use:   "purge [app-id]",
	Short: "Permanently remove an Agent App, its data, and database tables",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appID := args[0]

		mgr, cleanup, err := getPackageManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()

		if err := mgr.Purge(appID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if !globalFlags.JSONOut {
			fmt.Printf("✓ Purged Agent App '%s' (all data removed)\n", appID)
		}
	},
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed Agent Apps and their status",
	Run: func(cmd *cobra.Command, args []string) {
		mgr, cleanup, err := getPackageManager()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer cleanup()

		apps, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if globalFlags.JSONOut {
			_ = printJSON(os.Stdout, apps)
			return
		}

		if len(apps) == 0 {
			fmt.Println("No Agent Apps installed.")
			return
		}

		headers := []string{"APP ID", "NAME", "VERSION", "STATUS", "INSTALLED AT"}
		var rows [][]string
		for _, app := range apps {
			installedAt := time.Unix(app.InstalledAt, 0).Format("2006-01-02 15:04:05")
			rows = append(rows, []string{
				app.ID,
				app.Name,
				app.Version,
				app.Status,
				installedAt,
			})
		}

		printTable(headers, rows)
	},
}

// getPackageManager initializes and returns a Package Manager instance
// with a fresh SQLite connection and the global MCP registry.
func getPackageManager() (*packagemanager.Manager, func(), error) {
	dbPath := config.ResolvePath(globalFlags.DBPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}

	appsDir := config.ResolvePath(".tzro/apps")
	mgr := packagemanager.NewManager(db, mcp.GlobalRegistry, appsDir)

	if err := mgr.InitSchema(); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("failed to initialize package manager schema: %w", err)
	}

	cleanup := func() { db.Close() }
	return mgr, cleanup, nil
}

func init() {
	appCmd.AddCommand(appInstallCmd)
	appCmd.AddCommand(appUninstallCmd)
	appCmd.AddCommand(appPurgeCmd)
	appCmd.AddCommand(appListCmd)
	RootCmd.AddCommand(appCmd)
}
