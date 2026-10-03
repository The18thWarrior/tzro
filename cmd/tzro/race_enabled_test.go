//go:build race

package main

// Race instrumentation changes timing and memory use. The workloads still run,
// but CI checks their original limits separately without instrumentation.
const raceEnabled = true
