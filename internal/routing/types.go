package routing

// RoutingContext carries everything the router needs to make a decision.
// Built by the caller (task.Plan) from config + execution options.
type RoutingContext struct {
	Prompt              string   // The user's raw prompt text
	ActivePaths         []string // File/directory paths in the active workspace context
	ComplexityTier      string   // "T0" | "T1" | "T2" — from classifier
	PrivacyLevel        string   // "strict-local" | "hybrid" | "cloud-preferred"
	ComplexityThreshold string   // "T0" | "T1" | "T2" — from config
	RestrictedDirs      []string // From config
	SensitiveKeywords   []string // From config (or defaults)
	ModelMode           string   // "cooperative" | "local" | "cloud" — short-circuit override
	CloudKeyAvailable   bool     // Is a cloud API key configured?
	LocalBackendActive  bool     // Is the local inference backend running?
}

// RoutingDecision is the output of Route().
type RoutingDecision struct {
	Backend            string // "local" | "cloud"
	Reason             string // Human-readable explanation for telemetry/logging
	PrivacyQuarantined bool   // True if privacy constraints forced the decision
	AllowCloudFallback bool   // If local planning fails validation, can we escalate?
}
