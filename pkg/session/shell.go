package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tzro/pkg/store"
)

// SupportedShells lists the shells supported by tzro shell integration.
var SupportedShells = []string{"zsh", "bash"}

// DefaultAllowlistPrefixes lists the built-in commands allowed for automatic command capture.
var DefaultAllowlistPrefixes = []string{
	"go test", "go build", "go vet", "go run",
	"cargo test", "cargo build", "cargo check", "cargo clippy", "cargo run",
	"git commit", "git merge", "git checkout", "git switch", "git status",
	"git rebase", "git pull", "git push", "git diff", "git log", "git add",
	"git branch", "git stash", "git show", "git reset",
	"npm test", "npm run", "npm build",
	"pnpm test", "pnpm run", "pnpm build",
	"yarn test", "yarn run", "yarn build",
	"pytest", "python -m unittest", "python -m pytest",
	"python3 -m unittest", "python3 -m pytest",
	"make", "tsc", "tzro",
}

// Regex patterns for sensitive values
var (
	sensitiveEnvRegex  = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASS|AUTH|CRED)[A-Z0-9_]*)=([^\s]+)`)
	sensitiveFlagEq    = regexp.MustCompile(`(?i)(--(?:password|passwd|pass|token|auth-token|api-token|api-key|apikey|key|secret|secret-key|auth|authorization|credentials))=[^\s]+`)
	sensitiveFlagSpace = regexp.MustCompile(`(?i)(--(?:password|passwd|pass|token|auth-token|api-token|api-key|apikey|key|secret|secret-key|auth|authorization|credentials))\s+[^\s]+`)
	bearerRegex        = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9_\-\.]+`)
	ghpTokenRegex      = regexp.MustCompile(`\bghp_[a-zA-Z0-9]{20,}\b`)
	ghPatTokenRegex    = regexp.MustCompile(`\bgithub_pat_[a-zA-Z0-9_]+\b`)
	slackTokenRegex    = regexp.MustCompile(`\bxox[baprs]-[0-9a-zA-Z\-]+\b`)
	openAITokenRegex   = regexp.MustCompile(`\bsk-[a-zA-Z0-9\-_]{20,}\b`)
	privateKeyRegex    = regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----`)
)

// IsCommandAllowlisted evaluates if a command line should be captured.
// Ambiguous compound, background, or piped commands are excluded in the first slice.
func IsCommandAllowlisted(rawCmd string, customPatterns []string) bool {
	trimmed := strings.TrimSpace(rawCmd)
	if trimmed == "" {
		return false
	}

	// Reject compound / chained / background commands: ;, &&, ||, |, &, newline, carriage return, subshell parens
	for _, sep := range []string{";", "&&", "||", "|", "&", "\n", "\r", "`", "(", ")"} {
		if strings.Contains(trimmed, sep) {
			return false
		}
	}

	// Split into space-delimited tokens
	tokens := strings.Fields(trimmed)
	if len(tokens) == 0 {
		return false
	}

	// Skip leading environment variable assignments (e.g. CGO_ENABLED=0 FOO=bar go test)
	cmdTokens := tokens
	for len(cmdTokens) > 0 {
		first := cmdTokens[0]
		eqIdx := strings.Index(first, "=")
		if eqIdx > 0 && !strings.HasPrefix(first, "-") {
			cmdTokens = cmdTokens[1:]
		} else {
			break
		}
	}

	if len(cmdTokens) == 0 {
		return false
	}

	cleanCmd := strings.Join(cmdTokens, " ")

	// Check built-in prefixes
	for _, prefix := range DefaultAllowlistPrefixes {
		if cleanCmd == prefix || strings.HasPrefix(cleanCmd, prefix+" ") {
			return true
		}
	}

	// Check custom patterns from repository context configuration
	for _, pat := range customPatterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if cleanCmd == pat || strings.HasPrefix(cleanCmd, pat+" ") {
			return true
		}
		if matched, err := filepath.Match(pat, cleanCmd); err == nil && matched {
			return true
		}
		if matched, err := filepath.Match(pat, cmdTokens[0]); err == nil && matched {
			return true
		}
	}

	return false
}

// RedactCommand sanitizes sensitive credentials, environment assignments, and argument values.
func RedactCommand(rawCmd string) string {
	res := rawCmd

	// Redact environment variable assignments with sensitive names
	res = sensitiveEnvRegex.ReplaceAllString(res, "$1=[REDACTED]")

	// Redact sensitive flags (--flag=val and --flag val)
	res = sensitiveFlagEq.ReplaceAllString(res, "$1=[REDACTED]")
	res = sensitiveFlagSpace.ReplaceAllString(res, "$1 [REDACTED]")

	// Redact bearer tokens and known API key patterns
	res = bearerRegex.ReplaceAllString(res, "Bearer [REDACTED]")
	res = ghpTokenRegex.ReplaceAllString(res, "[REDACTED]")
	res = ghPatTokenRegex.ReplaceAllString(res, "[REDACTED]")
	res = slackTokenRegex.ReplaceAllString(res, "[REDACTED]")
	res = openAITokenRegex.ReplaceAllString(res, "[REDACTED]")
	res = privateKeyRegex.ReplaceAllString(res, "[REDACTED_PRIVATE_KEY]")

	return res
}

// GenerateShellInit emits the opt-in shell integration script for zsh or bash.
func GenerateShellInit(shellType string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(shellType))
	switch norm {
	case "zsh":
		return generateZshScript(), nil
	case "bash":
		return generateBashScript(), nil
	default:
		return "", fmt.Errorf("unsupported shell: %q (supported shells: zsh, bash)", shellType)
	}
}

func generateZshScript() string {
	return `# tzro shell integration for zsh
# Supported versions: zsh 5.0+
# Idempotent loading guard
if [[ -n "$_TZRO_ZSH_INIT" ]]; then
  return 0 2>/dev/null || true
fi
export _TZRO_ZSH_INIT=1

# Ensure a unique shell instance identifier
if [[ -z "$TZRO_SHELL_ID" ]]; then
  export TZRO_SHELL_ID="zsh_$(date +%s)_$$"
fi

autoload -Uz add-zsh-hook 2>/dev/null

_tzro_last_cmd_id=""

_tzro_preexec() {
  local cmd="$1"
  if ! command -v tzro >/dev/null 2>&1; then
    _tzro_last_cmd_id=""
    return 0
  fi
  _tzro_last_cmd_id="cmd_$(date +%s)_$$"
  tzro shell record preexec --shell-id="$TZRO_SHELL_ID" --id="$_tzro_last_cmd_id" --cmd="$cmd" >/dev/null 2>&1 || true
}

_tzro_precmd() {
  local exit_status=$?
  if [[ -n "$_tzro_last_cmd_id" ]]; then
    if command -v tzro >/dev/null 2>&1; then
      tzro shell record precmd --shell-id="$TZRO_SHELL_ID" --id="$_tzro_last_cmd_id" --status="$exit_status" >/dev/null 2>&1 || true
    fi
    _tzro_last_cmd_id=""
  fi
  return $exit_status
}

add-zsh-hook preexec _tzro_preexec
add-zsh-hook precmd _tzro_precmd
`
}

func generateBashScript() string {
	return `# tzro shell integration for bash
# Supported versions: Bash 3.2, 4.x, 5.x
# Idempotent loading guard
if [ -n "$_TZRO_BASH_INIT" ]; then
  return 0 2>/dev/null || true
fi
export _TZRO_BASH_INIT=1

# Ensure a unique shell instance identifier
if [ -z "$TZRO_SHELL_ID" ]; then
  export TZRO_SHELL_ID="bash_$(date +%s)_$$"
fi

_tzro_last_cmd_id=""
_tzro_preexec_done=0

_tzro_preexec() {
  if [ "$_tzro_preexec_done" -eq 1 ]; then
    return 0
  fi
  _tzro_preexec_done=1

  local cmd="$BASH_COMMAND"
  if [ -z "$cmd" ] || [ "$cmd" = "_tzro_precmd" ]; then
    return 0
  fi

  if ! command -v tzro >/dev/null 2>&1; then
    _tzro_last_cmd_id=""
    return 0
  fi

  _tzro_last_cmd_id="cmd_$(date +%s)_$$"
  tzro shell record preexec --shell-id="$TZRO_SHELL_ID" --id="$_tzro_last_cmd_id" --cmd="$cmd" >/dev/null 2>&1 || true
}

_tzro_precmd() {
  local exit_status=$?
  _tzro_preexec_done=0
  if [ -n "$_tzro_last_cmd_id" ]; then
    if command -v tzro >/dev/null 2>&1; then
      tzro shell record precmd --shell-id="$TZRO_SHELL_ID" --id="$_tzro_last_cmd_id" --status="$exit_status" >/dev/null 2>&1 || true
    fi
    _tzro_last_cmd_id=""
  fi
  return $exit_status
}

# Install DEBUG trap for preexec without replacing existing traps
_tzro_install_trap() {
  local existing_trap
  existing_trap=$(trap -p DEBUG | sed -e "s/^trap -- '//" -e "s/' DEBUG$//")
  if [ -z "$existing_trap" ]; then
    trap '_tzro_preexec' DEBUG
  elif [[ "$existing_trap" != *"_tzro_preexec"* ]]; then
    trap "_tzro_preexec; $existing_trap" DEBUG
  fi
}
_tzro_install_trap

# Append or prepend to PROMPT_COMMAND preserving string or array forms
_tzro_install_prompt_command() {
  if [[ "$(declare -p PROMPT_COMMAND 2>/dev/null)" =~ "declare -a" ]]; then
    local found=0
    for cmd in "${PROMPT_COMMAND[@]}"; do
      if [ "$cmd" = "_tzro_precmd" ]; then
        found=1
        break
      fi
    done
    if [ "$found" -eq 0 ]; then
      PROMPT_COMMAND=('_tzro_precmd' "${PROMPT_COMMAND[@]}")
    fi
  else
    if [ -z "$PROMPT_COMMAND" ]; then
      PROMPT_COMMAND="_tzro_precmd"
    elif [[ "$PROMPT_COMMAND" != *"_tzro_precmd"* ]]; then
      PROMPT_COMMAND="_tzro_precmd; $PROMPT_COMMAND"
    fi
  fi
}
_tzro_install_prompt_command
`
}

// ShellMetrics aggregates recorder health, latency measurements, and queue limits.
type ShellMetrics struct {
	ShellID              string  `json:"shell_id"`
	Workspace            string  `json:"workspace"`
	ActiveSessionID      string  `json:"active_session_id,omitempty"`
	ColdStartupMs        float64 `json:"cold_startup_ms"`
	EnqueueLatencyMs     float64 `json:"enqueue_latency_ms"`
	PersistenceLatencyMs float64 `json:"persistence_latency_ms"`
	EventLossCount       int     `json:"event_loss_count"`
	QueueMaxCount        int     `json:"queue_max_count"`
	QueueMaxAgeDays      int     `json:"queue_max_age_days"`
	ActiveTaskBound      bool    `json:"active_task_bound"`
}

// MeasureShellMetrics gathers diagnostic benchmarks for cold startup, enqueue check, and persistence latency.
func MeasureShellMetrics(workspaceRoot, shellID string, s *store.Store) ShellMetrics {
	startCold := time.Now()
	_ = IsCommandAllowlisted("go test ./...", nil)
	coldStartup := time.Since(startCold)

	startEnqueue := time.Now()
	for i := 0; i < 100; i++ {
		_ = IsCommandAllowlisted("cargo test --release", nil)
		_ = RedactCommand("go test --token=secret123")
	}
	enqueueLatency := time.Since(startEnqueue) / 100

	var persistLatency time.Duration
	var activeSess string
	var gapCount int

	if s != nil && workspaceRoot != "" {
		activeSess, _ = s.GetActiveTask(workspaceRoot, shellID)
		gapCount, _ = s.GetCaptureGapCount(workspaceRoot)

		testID := fmt.Sprintf("bench_probe_%d", time.Now().UnixNano())
		startPersist := time.Now()
		_ = s.RecordCommandStart(testID, workspaceRoot, "bench_session", shellID, "go test ./...", workspaceRoot, time.Now())
		_ = s.RecordCommandComplete(testID, 0, time.Now())
		persistLatency = time.Since(startPersist)

		// Clean up probe event
		_ = s.PruneCommandEvents(workspaceRoot, 1000, 30*24*time.Hour)
	}

	return ShellMetrics{
		ShellID:              shellID,
		Workspace:            workspaceRoot,
		ActiveSessionID:      activeSess,
		ColdStartupMs:        float64(coldStartup.Microseconds()) / 1000.0,
		EnqueueLatencyMs:     float64(enqueueLatency.Microseconds()) / 1000.0,
		PersistenceLatencyMs: float64(persistLatency.Microseconds()) / 1000.0,
		EventLossCount:       gapCount,
		QueueMaxCount:        1000,
		QueueMaxAgeDays:      30,
		ActiveTaskBound:      activeSess != "",
	}
}

// ConvertStoredEvent converts store.StoredCommandEvent to session.CommandEvent.
func ConvertStoredEvent(se store.StoredCommandEvent) CommandEvent {
	return CommandEvent{
		ID:          se.ID,
		DisplayText: se.DisplayText,
		Cwd:         se.Cwd,
		Workspace:   se.Workspace,
		SessionID:   se.SessionID,
		ShellID:     se.ShellID,
		StartedAt:   se.StartedAt,
		CompletedAt: se.CompletedAt,
		ExitStatus:  se.ExitStatus,
	}
}
