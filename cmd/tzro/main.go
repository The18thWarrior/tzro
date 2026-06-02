package main

import (
	"fmt"
	"os"
	"tzro/internal/cli"
)

func main() {
	if err := cli.RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Execution Error: %v\n", err)
		os.Exit(1)
	}
}
