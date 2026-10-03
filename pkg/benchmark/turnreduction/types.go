package turnreduction

import "time"

// EvaluationConfig selects the native client and immutable task inputs.
// Offline marks scripted-client evidence, which cannot establish a speedup.
type EvaluationConfig struct {
	InvocationRecorderPath string            `json:"invocation_recorder_path,omitempty"`
	ClientPath             string            `json:"client_path"`
	TzroPath               string            `json:"tzro_path"`
	HelperPath             string            `json:"helper_path"`
	TzroGuidancePath       string            `json:"tzro_guidance_path,omitempty"`
	OutputDir              string            `json:"output_dir"`
	Fixtures               []Fixture         `json:"-"`
	Conditions             []Condition       `json:"conditions"`
	Model                  string            `json:"model"`
	TaskTimeout            time.Duration     `json:"-"`
	Offline                bool              `json:"offline"`
	GuardPath              string            `json:"guard_path"`
	LedgerPath             string            `json:"ledger_path"`
	RunID                  string            `json:"run_id"`
	MaxLaunches            int               `json:"max_launches"`
	APIKey                 string            `json:"-"`
	Authorized             bool              `json:"authorized"`
	AcceptUnknownCost      bool              `json:"accept_unknown_cost"`
	ClientVersion          string            `json:"client_version"`
	Toolchain              map[string]string `json:"toolchain"`
	GoCache                string            `json:"go_cache"`
	AmendsContract         string            `json:"amends_contract,omitempty"`
	AmendmentReason        string            `json:"amendment_reason,omitempty"`
	contractHash           string
}

type EvaluationReport struct {
	InvocationRecorderSHA256 string            `json:"invocation_recorder_sha256,omitempty"`
	Version                  string            `json:"version"`
	Started                  time.Time         `json:"started"`
	Offline                  bool              `json:"offline"`
	Model                    string            `json:"model"`
	Cells                    []Observation     `json:"cells"`
	Summary                  EvaluationSummary `json:"summary"`
	Schedule                 []ScheduledCell   `json:"schedule"`
	ContractHash             string            `json:"contract_hash"`
	TzroGuidanceSHA256       string            `json:"tzro_guidance_sha256,omitempty"`
}

// Observation keeps unsuccessful elapsed time separate from verified completion.
type Observation struct {
	FixtureID                 string         `json:"fixture_id"`
	Condition                 Condition      `json:"condition"`
	Passed                    bool           `json:"passed"`
	PreparationSeconds        float64        `json:"preparation_s"`
	AgentSeconds              float64        `json:"agent_s"`
	GradeSeconds              float64        `json:"grade_s"`
	ElapsedSeconds            float64        `json:"elapsed_s"`
	VerifiedCompletionSeconds *float64       `json:"verified_completion_s"`
	ExecutionWorkspace        string         `json:"execution_workspace"`
	ArtifactDir               string         `json:"artifact_dir"`
	Events                    NativeEvents   `json:"events"`
	Checks                    []CheckReceipt `json:"final_checks"`
	Errors                    []string       `json:"errors,omitempty"`
	State                     string         `json:"state"`
	AgentExitCode             *int           `json:"agent_exit_code"`
}

type CheckReceipt struct {
	ID              string   `json:"id"`
	Argv            []string `json:"argv"`
	Passed          bool     `json:"passed"`
	ExitCode        *int     `json:"exit_code"`
	DurationSeconds float64  `json:"duration_s"`
	Log             string   `json:"log"`
}
