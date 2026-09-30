package workflow

import (
	"os"
	"path/filepath"
	"strings"
)

// hostTools preserves PATH precedence while excluding the product under test
// and the client, which is supplied explicitly. Homes and credentials remain
// isolated. This environment is not an operating-system security sandbox.
func hostTools(tzroBinary string) map[string]string {
	result := map[string]string{}
	product, _ := filepath.EvalSymlinks(tzroBinary)
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory == "" {
			continue
		}
		directory, err := filepath.Abs(directory)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if _, exists := result[name]; exists || profileTool(name) {
				continue
			}
			path := filepath.Join(directory, name)
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				continue
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || resolved == product || profileTool(filepath.Base(resolved)) {
				continue
			}
			result[name] = path
		}
	}
	return result
}

func profileTool(name string) bool {
	return name == "pi" || name == "tzro" || strings.HasPrefix(name, "tzro-") || name == "jev-score" || name == "gliner_worker.py"
}
