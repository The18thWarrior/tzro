package skills

import (
	"os"
	"path/filepath"
	"testing"

	"tzro/internal/memory"
)

func TestSynthesizeSOPDeduplication(t *testing.T) {
	// Initialize a temporary SQLite database for this integration test
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_skills.db")
	jsonPath := filepath.Join(tempDir, "test_skills_db.json")

	// Ensure any existing files are removed and we close any prior global DB
	_ = os.Remove(dbPath)
	_ = os.Remove(jsonPath)
	memory.DB.Close()

	// Set temporary test paths
	memory.DB.SetDBPathForTesting(dbPath)
	
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("Failed to initialize test SQLite DB: %v", err)
	}
	defer memory.DB.Close()

	// Verify database is starting empty
	initialSkills := memory.DB.GetSkills()
	if len(initialSkills) != 0 {
		t.Fatalf("Expected 0 initial skills in test DB, got %d", len(initialSkills))
	}

	// 1. Synthesize first skill
	skill1, err := SynthesizeSOP("task-100", "create a hubspot lead flow", []memory.NodeState{
		{NodeID: "fetch-leads", Status: "completed", Output: "Success"},
	})
	if err != nil {
		t.Fatalf("Failed to synthesize first skill: %v", err)
	}

	// Verify it was persisted
	skillsList := memory.DB.GetSkills()
	if len(skillsList) != 1 {
		t.Fatalf("Expected 1 skill in DB, got %d", len(skillsList))
	}
	if skillsList[0].ID != skill1.ID {
		t.Errorf("Expected skill ID %s, got %s", skill1.ID, skillsList[0].ID)
	}

	// 2. Synthesize second skill with a highly similar trigger (similarity >= 0.8)
	skill2, err := SynthesizeSOP("task-101", "create new hubspot lead flow!", []memory.NodeState{
		{NodeID: "fetch-leads-v2", Status: "completed", Output: "Success v2"},
	})
	if err != nil {
		t.Fatalf("Failed to synthesize second skill: %v", err)
	}

	// Verify that deduplication aborted insertion and returned the first skill
	if skill2.ID != skill1.ID {
		t.Errorf("Expected returned skill to be the existing one (ID %s), but got ID %s", skill1.ID, skill2.ID)
	}

	// Verify no new skill was inserted in SQLite
	skillsListAfterDup := memory.DB.GetSkills()
	if len(skillsListAfterDup) != 1 {
		t.Errorf("Expected skill list to still have size 1, but got %d", len(skillsListAfterDup))
	}

	// 3. Synthesize third skill with a completely different trigger (similarity < 0.8)
	skill3, err := SynthesizeSOP("task-102", "resolve customer ticket", []memory.NodeState{
		{NodeID: "fetch-ticket", Status: "completed", Output: "Ticket loaded"},
	})
	if err != nil {
		t.Fatalf("Failed to synthesize third skill: %v", err)
	}

	// Verify that the third skill was inserted as a unique skill
	if skill3.ID == skill1.ID {
		t.Errorf("Expected third skill to be a new skill, but it has the same ID as the first skill (%s)", skill1.ID)
	}

	// Verify database has exactly 2 skills now
	finalSkillsList := memory.DB.GetSkills()
	if len(finalSkillsList) != 2 {
		t.Errorf("Expected final skill list to have size 2, but got %d", len(finalSkillsList))
	}

	// Double check identities of both skills in SQLite
	var found1, found3 bool
	for _, s := range finalSkillsList {
		if s.ID == skill1.ID {
			found1 = true
		}
		if s.ID == skill3.ID {
			found3 = true
		}
	}
	if !found1 || !found3 {
		t.Errorf("Expected both skill1 and skill3 to be in final SQLite database. Found1: %v, Found3: %v", found1, found3)
	}
}
