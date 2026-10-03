package turnreduction

import (
	"os"
	"path/filepath"
)

func installSimpleHelper(source, workspace string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(workspace, ".tools"), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(workspace, ".tools", "bulk_update.py"), data, 0644); err != nil {
		return err
	}
	const guidance = "\nA bulk replacement helper is available: `python3 .tools/bulk_update.py <old_text> <new_text> <file1> ...`. It replaces caller-supplied literal text in the selected files. It does not run tests. You may use it when useful.\n"
	return appendAgentGuidance(workspace, []byte(guidance))
}

// appendAgentGuidance preserves project instructions and makes a declared
// instruction treatment visible through the native client's ordinary rules.
func appendAgentGuidance(workspace string, guidance []byte) error {
	file, err := os.OpenFile(filepath.Join(workspace, "AGENTS.md"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(guidance)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
