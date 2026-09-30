package dlp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// PolicyAction defines what happens when a rule matches.
type PolicyAction string

const (
	ActionAllow  PolicyAction = "allow"
	ActionDeny   PolicyAction = "deny"
	ActionRedact PolicyAction = "redact"
	ActionBlock  PolicyAction = "block"
)

// PolicyRule defines an individual policy rule.
type PolicyRule struct {
	PathPattern string       `json:"path_pattern,omitempty"` // Glob or prefix path pattern
	DataClass   string       `json:"data_class,omitempty"`   // e.g. "api_key", "secret", "private_key", "*"
	Action      PolicyAction `json:"action"`                 // allow, deny, redact, block
	Description string       `json:"description,omitempty"`
}

// WorkspacePolicy represents a full workspace-level privacy policy.
type WorkspacePolicy struct {
	Version       string       `json:"version"`
	DefaultAction PolicyAction `json:"default_action"` // default: allow or redact
	Rules         []PolicyRule `json:"rules"`
}

// PolicyEvaluation is the result of evaluating content or path against the policy.
type PolicyEvaluation struct {
	Allowed     bool         `json:"allowed"`
	Action      PolicyAction `json:"action"`
	MatchedRule *PolicyRule  `json:"matched_rule,omitempty"`
	Reason      string       `json:"reason"`
	Path        string       `json:"path,omitempty"`
	DataClass   string       `json:"data_class,omitempty"`
}

// PolicyEngine evaluates requests against workspace privacy policies.
type PolicyEngine struct {
	mu     sync.RWMutex
	policy *WorkspacePolicy
}

// NewPolicyEngine creates a new policy engine with default rules.
func NewPolicyEngine(p *WorkspacePolicy) *PolicyEngine {
	if p == nil {
		p = &WorkspacePolicy{
			Version:       "1.0",
			DefaultAction: ActionAllow,
			Rules:         []PolicyRule{},
		}
	}
	return &PolicyEngine{
		policy: p,
	}
}

// LoadWorkspacePolicy loads policy from workspace path (e.g. .tzro/privacy.json) or returns a default.
func LoadWorkspacePolicy(workspaceRoot string) (*WorkspacePolicy, error) {
	policyFile := filepath.Join(workspaceRoot, ".tzro", "privacy.json")
	data, err := os.ReadFile(policyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &WorkspacePolicy{
				Version:       "1.0",
				DefaultAction: ActionAllow,
				Rules: []PolicyRule{
					{PathPattern: ".env*", Action: ActionBlock, Description: "Block all environment files"},
					{PathPattern: "*secret*", Action: ActionBlock, Description: "Block files with secret in path"},
					{PathPattern: "*id_rsa*", Action: ActionBlock, Description: "Block ssh private keys"},
				},
			}, nil
		}
		return nil, err
	}

	var p WorkspacePolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("malformed privacy.json: %w", err)
	}
	return &p, nil
}

// MatchPath checks if a path matches a pattern.
func MatchPath(pattern, path string) bool {
	matched, err := filepath.Match(pattern, path)
	if err == nil && matched {
		return true
	}
	// Also test filename base
	matched, err = filepath.Match(pattern, filepath.Base(path))
	if err == nil && matched {
		return true
	}
	// Case-insensitive substring match as fallback for glob patterns like *secret*
	cleanPattern := strings.Trim(pattern, "*")
	if cleanPattern != "" && strings.Contains(strings.ToLower(path), strings.ToLower(cleanPattern)) {
		return true
	}
	return false
}

// EvaluatePath checks if a file path is permitted to egress.
func (pe *PolicyEngine) EvaluatePath(path string) PolicyEvaluation {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	for _, rule := range pe.policy.Rules {
		if rule.PathPattern != "" && MatchPath(rule.PathPattern, path) {
			switch rule.Action {
			case ActionBlock, ActionDeny:
				return PolicyEvaluation{
					Allowed:     false,
					Action:      rule.Action,
					MatchedRule: &rule,
					Path:        path,
					Reason:      fmt.Sprintf("egress blocked: path %q matches rule %q (%s)", path, rule.PathPattern, rule.Action),
				}
			case ActionAllow:
				return PolicyEvaluation{
					Allowed:     true,
					Action:      ActionAllow,
					MatchedRule: &rule,
					Path:        path,
					Reason:      fmt.Sprintf("path %q allowed by rule", path),
				}
			case ActionRedact:
				return PolicyEvaluation{
					Allowed:     true,
					Action:      ActionRedact,
					MatchedRule: &rule,
					Path:        path,
					Reason:      fmt.Sprintf("path %q marked for redaction", path),
				}
			}
		}
	}

	// Fall back to default action
	if pe.policy.DefaultAction == ActionBlock || pe.policy.DefaultAction == ActionDeny {
		return PolicyEvaluation{
			Allowed: false,
			Action:  pe.policy.DefaultAction,
			Path:    path,
			Reason:  fmt.Sprintf("egress blocked: path %q rejected by default policy %s", path, pe.policy.DefaultAction),
		}
	}

	return PolicyEvaluation{
		Allowed: true,
		Action:  pe.policy.DefaultAction,
		Path:    path,
		Reason:  "allowed by default workspace policy",
	}
}

// EvaluateContent scans text for path mentions or sensitive data categories and checks policy actions.
func (pe *PolicyEngine) EvaluateContent(text string) PolicyEvaluation {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	// Path rules apply to path references, not arbitrary prose or identifiers.
	// Credential data-class rules below still inspect the complete content.
	paths := contentPaths(text)
	for _, rule := range pe.policy.Rules {
		if rule.PathPattern != "" {
			for _, path := range paths {
				if MatchPath(rule.PathPattern, path) && (rule.Action == ActionBlock || rule.Action == ActionDeny) {
					return PolicyEvaluation{
						Allowed:     false,
						Action:      rule.Action,
						MatchedRule: &rule,
						Path:        path,
						Reason:      fmt.Sprintf("egress blocked: content references path %q matching %q", path, rule.PathPattern),
					}
				}
			}
		}

		if rule.DataClass != "" && (rule.Action == ActionBlock || rule.Action == ActionDeny) {
			hasSecret := false
			switch rule.DataClass {
			case "api_key", "openai_key":
				hasSecret = openAIKeyRe.MatchString(text)
			case "github_token":
				hasSecret = githubPatRe.MatchString(text)
			case "aws_key":
				hasSecret = awsKeyRe.MatchString(text)
			case "jwt":
				hasSecret = jwtRe.MatchString(text)
			case "private_key":
				hasSecret = privKeyRe.MatchString(text)
			case "*", "all":
				hasSecret = openAIKeyRe.MatchString(text) || githubPatRe.MatchString(text) ||
					awsKeyRe.MatchString(text) || jwtRe.MatchString(text) || privKeyRe.MatchString(text)
			}

			if hasSecret {
				return PolicyEvaluation{
					Allowed:     false,
					Action:      rule.Action,
					MatchedRule: &rule,
					DataClass:   rule.DataClass,
					Reason:      fmt.Sprintf("egress blocked: content matches blocked data class %q", rule.DataClass),
				}
			}
		}
	}

	return PolicyEvaluation{
		Allowed: true,
		Action:  pe.policy.DefaultAction,
		Reason:  "allowed by policy",
	}
}

var contentPathToken = regexp.MustCompile(`[\pL\pN_./\\~:-]+`)

// Decode native request/message JSON before looking for paths so escaped paths
// and tool arguments use the same policy as plain text. Bare words are paths
// only in explicit path fields; "secret" in prose is not a file reference.
func contentPaths(text string) []string {
	var paths []string
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				switch strings.ToLower(key) {
				case "path", "file_path", "filepath", "filename":
					if path, ok := item.(string); ok {
						paths = append(paths, path)
					}
				}
				visit(item)
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		case string:
			var decoded any
			if json.Unmarshal([]byte(v), &decoded) == nil {
				visit(decoded)
				return
			}
			for _, token := range contentPathToken.FindAllString(v, -1) {
				token = strings.TrimRight(token, ".:")
				if strings.ContainsAny(token, `/\\.`) && strings.Trim(token, ".") != "" {
					paths = append(paths, token)
				}
			}
		}
	}
	visit(text)
	return paths
}
