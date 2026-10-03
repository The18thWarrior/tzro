package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// Emit ready line matching the real worker protocol
	fmt.Println(`{"status":"ready"}`)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		resp := map[string]interface{}{
			"answer":     "yes",
			"confidence": 0.5,
			"latency_ms": 5,
		}
		b, _ := json.Marshal(resp)
		fmt.Println(string(b))
		os.Exit(0)
	}
}
