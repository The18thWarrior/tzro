package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	startPort := 8080
	if len(os.Args) > 1 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil {
			startPort = p
		}
	}

	listener, port, err := getListener(startPort)
	if err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

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
	fmt.Printf(" address: http://localhost:%d\n", port)
	fmt.Printf("========================================================\n")
	fmt.Printf("Press Ctrl+C to terminate\n\n")

	if err := http.Serve(listener, nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}

func getListener(startPort int) (net.Listener, int, error) {
	if startPort <= 0 {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, 0, err
		}
		return ln, ln.Addr().(*net.TCPAddr).Port, nil
	}

	// Try from startPort up to startPort + 50
	for p := startPort; p < startPort+50; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			return ln, p, nil
		}
	}

	// Fallback to any available port assigned by the OS
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

