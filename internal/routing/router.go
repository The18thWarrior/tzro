package routing

import (
	"path/filepath"
	"strings"
)

// Route evaluates the routing context and returns a decision on whether to plan
// locally or in the cloud. Implements a short-circuit decision tree:
//
//	Gate 0: ModelMode override
//	Gate 1: Privacy quarantine (restricted dirs + sensitive keywords)
//	Gate 2: Cloud availability
//	Gate 3: Complexity threshold
//	Gate 4: strict-local safety net
//	Default: cloud
func Route(ctx RoutingContext) RoutingDecision {
	// Gate 0: ModelMode override (developer short-circuit)
	if ctx.ModelMode == "local" {
		return RoutingDecision{
			Backend:            "local",
			Reason:             "ModelMode forced to local",
			AllowCloudFallback: false,
		}
	}
	if ctx.ModelMode == "cloud" {
		return RoutingDecision{
			Backend:            "cloud",
			Reason:             "ModelMode forced to cloud",
			AllowCloudFallback: false,
		}
	}

	// Gate 1: Privacy quarantine check
	if quarantined, reason := isPrivacyQuarantined(ctx); quarantined {
		return RoutingDecision{
			Backend:            "local",
			Reason:             reason,
			PrivacyQuarantined: true,
			AllowCloudFallback: false,
		}
	}

	// Gate 2: Availability guard — if no cloud key, must go local
	if !ctx.CloudKeyAvailable {
		return RoutingDecision{
			Backend:            "local",
			Reason:             "No cloud API key configured",
			AllowCloudFallback: false,
		}
	}

	// Gate 3: Complexity threshold routing
	if tierAtOrBelow(ctx.ComplexityTier, ctx.ComplexityThreshold) {
		return RoutingDecision{
			Backend:            "local",
			Reason:             "Complexity at/below threshold",
			AllowCloudFallback: true,
		}
	}

	// Gate 4: strict-local safety net (catches edge cases where privacy wasn't
	// triggered by path/keyword but the level itself is restrictive)
	if ctx.PrivacyLevel == "strict-local" {
		return RoutingDecision{
			Backend:            "local",
			Reason:             "strict-local privacy level",
			PrivacyQuarantined: true,
			AllowCloudFallback: false,
		}
	}

	// Default: cloud planning with fallback allowed
	return RoutingDecision{
		Backend:            "cloud",
		Reason:             "Complexity above threshold, cloud permitted",
		AllowCloudFallback: true,
	}
}

// isPrivacyQuarantined checks if the task context triggers a privacy quarantine.
// Returns (true, reason) if any active path matches a restricted directory or
// the prompt contains a sensitive keyword.
func isPrivacyQuarantined(ctx RoutingContext) (bool, string) {
	// Check restricted directory matches
	for _, activePath := range ctx.ActivePaths {
		cleanActive := filepath.Clean(activePath)
		for _, restricted := range ctx.RestrictedDirs {
			cleanRestricted := filepath.Clean(restricted)
			// Check if the active path is within the restricted directory
			if strings.HasPrefix(cleanActive, cleanRestricted+string(filepath.Separator)) || cleanActive == cleanRestricted {
				return true, "Active path " + activePath + " matches restricted directory " + restricted
			}
		}
	}

	// Check sensitive keyword matches
	lowerPrompt := strings.ToLower(ctx.Prompt)
	for _, keyword := range ctx.SensitiveKeywords {
		if strings.Contains(lowerPrompt, strings.ToLower(keyword)) {
			return true, "Prompt contains sensitive keyword: " + keyword
		}
	}

	return false, ""
}

// tierAtOrBelow returns true if the actual complexity tier is at or below the threshold.
// Uses ordinal comparison: T0 < T1 < T2.
func tierAtOrBelow(actual, threshold string) bool {
	tierOrd := map[string]int{"T0": 0, "T1": 1, "T2": 2}

	actualOrd, okActual := tierOrd[actual]
	thresholdOrd, okThreshold := tierOrd[threshold]

	// If either tier is unrecognized, default to not matching (fall through to cloud)
	if !okActual || !okThreshold {
		return false
	}

	return actualOrd <= thresholdOrd
}
