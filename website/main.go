package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := "8080"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	// Determine the serving directory based on the location of main.go
	dir, err := filepath.Abs(".")
	if err != nil {
		fmt.Printf("Error resolving serving directory: %v\n", err)
		dir = "."
	}

	// If run from repository root, shift path to serve the website directory directly
	if _, err := os.Stat(filepath.Join(dir, "website", "index.html")); err == nil {
		dir = filepath.Join(dir, "website")
	}

	fs := http.FileServer(http.Dir(dir))
	http.Handle("/", fs)

	fmt.Printf("========================================================\n")
	fmt.Printf(" tzro website prototype server is active\n")
	fmt.Printf(" serving static assets from: %s\n", dir)
	fmt.Printf(" address: http://localhost:%s\n", port)
	fmt.Printf("========================================================\n")
	fmt.Printf("Press Ctrl+C to terminate\n\n")

	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}
