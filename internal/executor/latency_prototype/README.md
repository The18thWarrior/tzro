# Latency Prototype: Execution Sleep Bottlenecks

This prototype explores why executions in `tzro` take so long to finish and brainstorms ideas on how we can remediate.

## The Question
How do hardcoded execution sleeps impact overall DAG execution latency across different topologies, and how can we safely optimize or configure them away without losing the visual updates required by the GUI/TUI?

## Running the Prototype
To start the interactive logic prototype, run the following command from the repository root:

```bash
go run internal/executor/latency_prototype/main.go
```

## Structure
- `main.go`: Interactive Bubble Tea TUI simulating DAG execution under different sleep profiles and graph topologies.
- `NOTES.md`: Findings and remediation recommendations.
