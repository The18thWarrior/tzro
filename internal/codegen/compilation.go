package codegen

import (
	"path/filepath"
)

// CompilationCommand returns the compilation/type-check command for the given
// language and the file path. The command may contain the {{targetFile}}
// placeholder which should be replaced with the actual file path by the caller.
//
// Returns available=false for languages without a known compilation command,
// in which case the compilation gate should be skipped gracefully.
func CompilationCommand(language, filePath string) (command string, available bool) {
	switch language {
	case "go":
		// Go builds at the package level. Use ./... from the file's directory
		// so all files in the package are compiled together.
		dir := filepath.Dir(filePath)
		return "go build -o /dev/null " + dir + "/...", true

	case "typescript":
		// TypeScript type-check only (no emit). Use --noEmit --strict with
		// --target es2020 so async/await, Map, Set, Promise, WeakMap etc. are
		// valid (tsc defaults to es5 without a tsconfig, which rejects them).
		return "npx tsc --noEmit --strict --target es2020 --lib es2020 --moduleResolution node {{targetFile}}", true

	case "javascript":
		// JavaScript has no compiler, but we can syntax-check with Node.
		return "node --check {{targetFile}}", true

	case "python":
		// Python syntax check via the built-in py_compile module.
		return "python3 -m py_compile {{targetFile}}", true

	case "rust":
		// Rust builds via Cargo from the workspace root.
		return "cargo build", true

	default:
		return "", false
	}
}
