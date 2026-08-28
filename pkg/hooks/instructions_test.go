package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTzroSkill_NewFile(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteTzroSkill(skillsDir); err != nil {
		t.Fatalf("WriteTzroSkill failed: %v", err)
	}

	skillFile := filepath.Join(skillsDir, "tzro", "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("SKILL.md not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "name: tzro") {
		t.Error("expected YAML frontmatter with name")
	}
	if !strings.Contains(content, "description:") {
		t.Error("expected YAML frontmatter with description")
	}
	if !strings.Contains(content, "tzro probe") {
		t.Error("expected CLI reference content")
	}
	if !strings.Contains(content, "tzro expand") {
		t.Error("expected expand command in CLI reference")
	}
}

func TestWriteTzroSkill_Idempotent(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")

	if err := WriteTzroSkill(skillsDir); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(skillsDir, "tzro", "SKILL.md"))

	if err := WriteTzroSkill(skillsDir); err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(skillsDir, "tzro", "SKILL.md"))

	if string(first) != string(second) {
		t.Errorf("idempotency violated: content differs after second write")
	}
}

func TestWriteTzroInstructions_NewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "copilot-instructions.md")

	if err := WriteTzroInstructions(target); err != nil {
		t.Fatalf("WriteTzroInstructions failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, tzroInstructionsBeginMarker) {
		t.Error("expected begin marker in output")
	}
	if !strings.Contains(content, tzroInstructionsEndMarker) {
		t.Error("expected end marker in output")
	}
	if !strings.Contains(content, "tzro probe") {
		t.Error("expected CLI reference content")
	}
}

func TestWriteTzroInstructions_Idempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tzro.md")

	if err := WriteTzroInstructions(target); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	first, _ := os.ReadFile(target)

	if err := WriteTzroInstructions(target); err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	second, _ := os.ReadFile(target)

	if string(first) != string(second) {
		t.Errorf("idempotency violated: content differs after second write\nfirst (%d bytes)\nsecond (%d bytes)",
			len(first), len(second))
	}
}

func TestWriteTzroInstructions_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "copilot-instructions.md")

	existing := "# My Project\n\nUse gofmt for formatting.\n"
	if err := os.WriteFile(target, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteTzroInstructions(target); err != nil {
		t.Fatalf("WriteTzroInstructions failed: %v", err)
	}

	data, _ := os.ReadFile(target)
	content := string(data)

	if !strings.HasPrefix(content, existing) {
		t.Errorf("expected existing content preserved at start, got:\n%s", content[:100])
	}
	if !strings.Contains(content, tzroInstructionsBeginMarker) {
		t.Error("expected tzro block appended")
	}
}

func TestWriteTzroInstructions_ReplacesExistingBlock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rules.md")

	// Write a file with a stale tzro block sandwiched between other content
	staleBlock := "<!-- BEGIN TZRO INSTRUCTIONS -->\nOLD STALE CONTENT\n<!-- END TZRO INSTRUCTIONS -->\n"
	content := "# Before\n\n" + staleBlock + "\n# After\n"
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteTzroInstructions(target); err != nil {
		t.Fatalf("WriteTzroInstructions failed: %v", err)
	}

	data, _ := os.ReadFile(target)
	result := string(data)

	if strings.Contains(result, "OLD STALE CONTENT") {
		t.Error("stale content should have been replaced")
	}
	if !strings.Contains(result, "# Before") {
		t.Error("content before block should be preserved")
	}
	if !strings.Contains(result, "# After") {
		t.Error("content after block should be preserved")
	}
	if !strings.Contains(result, "tzro probe") {
		t.Error("new instruction block should be present")
	}

	// Ensure only one begin marker
	if strings.Count(result, tzroInstructionsBeginMarker) != 1 {
		t.Errorf("expected exactly one begin marker, got %d", strings.Count(result, tzroInstructionsBeginMarker))
	}
}

func TestUpsertInstructionBlock_EmptyString(t *testing.T) {
	block := tzroInstructionsBeginMarker + "\n\n" + tzroSkillMD + tzroInstructionsEndMarker + "\n"
	result := upsertInstructionBlock("", block)
	if !strings.Contains(result, tzroInstructionsBeginMarker) {
		t.Error("expected block in result")
	}
}

func TestUpsertInstructionBlock_NoTrailingNewline(t *testing.T) {
	block := tzroInstructionsBeginMarker + "\n\n" + tzroSkillMD + tzroInstructionsEndMarker + "\n"
	result := upsertInstructionBlock("some content", block)
	if !strings.Contains(result, "some content\n\n"+tzroInstructionsBeginMarker) {
		t.Errorf("expected blank line separator before block, got:\n%s", result[:80])
	}
}
