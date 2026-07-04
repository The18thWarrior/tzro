package codegen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverModuleContext scans the filesystem around filePath to discover
// available packages and module information. Returns a prompt-ready description
// that can be injected into codegen prompts to prevent import hallucination.
//
// Walks upward from filePath looking for language-specific manifests:
//   - Go: go.mod (extracts module path + require directives)
//   - TypeScript/JavaScript: package.json (extracts dependencies)
//   - Python: requirements.txt (extracts package names)
//
// Returns a stdlib-only fallback when no manifest is found.
func DiscoverModuleContext(filePath, language string) string {
	dir := filepath.Dir(filePath)

	switch language {
	case "go":
		return discoverGoContext(dir)
	case "typescript", "javascript":
		return discoverNodeContext(dir)
	case "python":
		return discoverPythonContext(dir)
	default:
		return stdlibFallback(language)
	}
}

// discoverGoContext walks up from dir looking for go.mod.
func discoverGoContext(dir string) string {
	modPath := findFileUpward(dir, "go.mod")
	if modPath == "" {
		return stdlibFallback("go")
	}

	data, err := os.ReadFile(modPath)
	if err != nil {
		return stdlibFallback("go")
	}

	content := string(data)
	var sb strings.Builder
	sb.WriteString("Go module context:\n")

	// Extract module path
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			sb.WriteString(fmt.Sprintf("- Module: %s\n", strings.TrimPrefix(line, "module ")))
			break
		}
	}

	// Extract require directives
	deps := extractGoRequires(content)
	if len(deps) > 0 {
		sb.WriteString("- Available dependencies:\n")
		for _, dep := range deps {
			sb.WriteString(fmt.Sprintf("  - %s\n", dep))
		}
	} else {
		sb.WriteString("- No third-party dependencies declared.\n")
	}

	sb.WriteString("- Go standard library is always available.\n")
	sb.WriteString("- Do NOT import packages not listed above.\n")
	return sb.String()
}

// extractGoRequires parses require blocks and single-line requires from go.mod.
func extractGoRequires(content string) []string {
	var deps []string
	lines := strings.Split(content, "\n")
	inRequireBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "require (" {
			inRequireBlock = true
			continue
		}
		if inRequireBlock {
			if trimmed == ")" {
				inRequireBlock = false
				continue
			}
			// Lines like: github.com/google/uuid v1.6.0
			parts := strings.Fields(trimmed)
			if len(parts) >= 1 && !strings.HasPrefix(trimmed, "//") {
				deps = append(deps, parts[0])
			}
			continue
		}

		// Single-line require: require github.com/foo v1.0.0
		if strings.HasPrefix(trimmed, "require ") && !strings.Contains(trimmed, "(") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				deps = append(deps, parts[1])
			}
		}
	}

	return deps
}

// discoverNodeContext walks up from dir looking for package.json.
func discoverNodeContext(dir string) string {
	pkgPath := findFileUpward(dir, "package.json")
	if pkgPath == "" {
		return stdlibFallback("typescript")
	}

	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return stdlibFallback("typescript")
	}

	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return stdlibFallback("typescript")
	}

	var sb strings.Builder
	sb.WriteString("Node.js/TypeScript module context:\n")
	if pkg.Name != "" {
		sb.WriteString(fmt.Sprintf("- Package: %s\n", pkg.Name))
	}

	if len(pkg.Dependencies) > 0 {
		sb.WriteString("- Available dependencies:\n")
		for name := range pkg.Dependencies {
			sb.WriteString(fmt.Sprintf("  - %s\n", name))
		}
	}
	if len(pkg.DevDependencies) > 0 {
		sb.WriteString("- Dev dependencies:\n")
		for name := range pkg.DevDependencies {
			sb.WriteString(fmt.Sprintf("  - %s\n", name))
		}
	}

	if len(pkg.Dependencies) == 0 && len(pkg.DevDependencies) == 0 {
		sb.WriteString("- No third-party dependencies declared.\n")
	}

	sb.WriteString("- Node.js built-in modules are always available.\n")
	sb.WriteString("- Do NOT import packages not listed above.\n")
	return sb.String()
}

// discoverPythonContext walks up from dir looking for requirements.txt.
func discoverPythonContext(dir string) string {
	reqPath := findFileUpward(dir, "requirements.txt")
	if reqPath == "" {
		return stdlibFallback("python")
	}

	data, err := os.ReadFile(reqPath)
	if err != nil {
		return stdlibFallback("python")
	}

	var deps []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip version specifier: package>=1.0.0 → package
		for _, sep := range []string{">=", "<=", "==", "~=", "!="} {
			if idx := strings.Index(line, sep); idx > 0 {
				line = line[:idx]
				break
			}
		}
		deps = append(deps, strings.TrimSpace(line))
	}

	var sb strings.Builder
	sb.WriteString("Python module context:\n")
	if len(deps) > 0 {
		sb.WriteString("- Available packages:\n")
		for _, dep := range deps {
			sb.WriteString(fmt.Sprintf("  - %s\n", dep))
		}
	} else {
		sb.WriteString("- No third-party packages declared.\n")
	}
	sb.WriteString("- Python standard library is always available.\n")
	sb.WriteString("- Do NOT import packages not listed above.\n")
	return sb.String()
}

// findFileUpward walks upward from dir looking for a file with the given name.
// Returns the full path if found, or empty string if not found.
// Stops at filesystem root to prevent infinite traversal.
func findFileUpward(dir, filename string) string {
	for {
		candidate := filepath.Join(dir, filename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			return ""
		}
		dir = parent
	}
}

// stdlibFallback returns the default context for a language when no manifest is found.
func stdlibFallback(language string) string {
	return fmt.Sprintf("Standard library only — do not import third-party packages. Language: %s.", language)
}
