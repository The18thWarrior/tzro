package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// breadthThreshold is the minimum number of subdirectories under PreloadPaths
// that triggers breadth mode. When exceeded, the Kahn Compiler injects a
// shallow directory manifest into the probe's context and scales the step budget.
const breadthThreshold = 5

// DetectBreadthMode checks the PreloadPaths for breadth characteristics.
// If the total number of immediate subdirectories across all paths exceeds
// breadthThreshold, it returns isBreadth=true with a subdirCount and a
// formatted manifest listing all subdirectory names.
//
// Gracefully degrades: non-existent paths are ignored, files are excluded.
func DetectBreadthMode(preloadPaths []string) (isBreadth bool, subdirCount int, manifest string) {
	var allSubdirs []string

	for _, path := range preloadPaths {
		entries, err := os.ReadDir(path)
		if err != nil {
			// Graceful degradation — skip unreadable paths
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				subdirCount++
				allSubdirs = append(allSubdirs, entry.Name())
			}
		}
	}

	isBreadth = subdirCount > breadthThreshold

	if isBreadth && len(allSubdirs) > 0 {
		sort.Strings(allSubdirs)
		manifest = strings.Join(allSubdirs, "\n")
	}

	return isBreadth, subdirCount, manifest
}

// ScaleStepBudget calculates the step budget for a probe based on subdirectory count.
// Formula: baseBudget + subdirCount × 2, capped at maxBudget.
//
// Example: base=24, subdirs=15 → 54; base=24, subdirs=30 → 60 (capped)
func ScaleStepBudget(baseBudget, subdirCount, maxBudget int) int {
	scaled := baseBudget + subdirCount*2
	if scaled > maxBudget {
		return maxBudget
	}
	return scaled
}

// BuildBreadthManifest formats a directory manifest for injection into the
// probe's system prompt. Lists all subdirectories with a brief preamble.
func BuildBreadthManifest(rootPath, manifest string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Directory Manifest (%s)\n", filepath.Base(rootPath)))
	sb.WriteString("This codebase has a broad directory structure. Plan your exploration to cover these packages efficiently:\n\n")
	sb.WriteString(manifest)
	sb.WriteString("\n")
	return sb.String()
}
