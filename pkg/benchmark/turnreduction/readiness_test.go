package turnreduction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareEvaluation_RejectsUndiscoveredNativeMCP(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, `
import sys
if sys.argv[1:]==['--version']: print('1.2.15')
elif sys.argv[1:]==['mcp','list']: print('No MCP servers configured.')
else: raise RuntimeError('provider request forbidden in readiness')
`)
	guard, err := DefaultGuardPath(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := PrepareEvaluation(context.Background(), EvaluationConfig{ClientPath: client, TzroPath: binary, HelperPath: filepath.Join(filepath.Dir(guard), "bulk_update.py"), GuardPath: guard, Fixtures: fixtures})
	if result.OfflineReady || !strings.Contains(strings.Join(result.Problems, " "), "MCP") {
		t.Fatalf("invisible MCP accepted: %+v", result)
	}
}
