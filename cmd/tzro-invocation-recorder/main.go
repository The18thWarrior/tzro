// tzro-invocation-recorder is a passive, standalone Antigravity lifecycle observer.
package main

import (
	"flag"
	"fmt"
	"os"

	"tzro/pkg/benchmark/turnreduction/invocation"
)

func main() {
	kind := flag.String("event", "", "Native hook event")
	path := flag.String("output", "", "Append-only metadata evidence file")
	flag.Parse()
	if err := invocation.Record(*kind, *path, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "invocation observer:", err)
		os.Exit(1)
	}
}
