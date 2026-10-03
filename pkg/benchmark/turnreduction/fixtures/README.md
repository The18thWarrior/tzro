# Turn reduction fixture assets

These directories contain benchmark inputs, not packages in the main Tzro module.
The module boundary keeps `go test ./...` from compiling private graders without their subject files.

Run `go test ./pkg/benchmark/turnreduction` from the repository root to validate all nine fixtures.
Those tests copy each subject into an isolated workspace, prove its initial failure, apply reference edits,
and run the prescribed checks and every required private test.

The parent module file is not copied into task workspaces or grading copies.
Each Go subject keeps its own module file.
