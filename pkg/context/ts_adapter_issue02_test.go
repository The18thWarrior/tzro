package context_test

import (
	stdctx "context"
	"os"
	"path/filepath"
	"testing"

	tzroctx "tzro/pkg/context"
)

// TestIssue02_TSCrossFileAliasesAndBarrels tests:
// "Cross-file discovery covers aliases, namespace/default imports, re-exports, and cyclic barrels."
func TestIssue02_TSCrossFileAliasesAndBarrels(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Definition in src/math.ts
	_ = os.MkdirAll(filepath.Join(tempDir, "src"), 0755)
	mathCode := `export function computeTotal(a: number, b: number): number {
	return a + b;
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "math.ts"), []byte(mathCode), 0644)

	// 2. Barrel file re-export in src/index.ts
	barrelCode := `export * from './math';
export * from './cyclic';
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "index.ts"), []byte(barrelCode), 0644)

	// 3. Cyclic barrel in src/cyclic.ts referencing src/index.ts
	cyclicCode := `export * from './index';
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "cyclic.ts"), []byte(cyclicCode), 0644)

	// 4. Consumer app1: import with alias
	consumer1Code := `import { computeTotal as sum } from './index';

export function runCalculation() {
	return sum(10, 20);
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "app1.ts"), []byte(consumer1Code), 0644)

	// 5. Consumer app2: namespace import
	consumer2Code := `import * as MathOps from './math';

export function runNS() {
	return MathOps.computeTotal(5, 5);
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "app2.ts"), []byte(consumer2Code), 0644)

	// 6. Consumer app3: test file
	testCode := `import { computeTotal } from './index';

describe('math tests', () => {
	it('calculates sum', () => {
		expect(computeTotal(1, 2)).toBe(3);
	});
});
`
	_ = os.WriteFile(filepath.Join(tempDir, "src", "math.test.ts"), []byte(testCode), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	sym := tzroctx.Symbol{
		Name:      "computeTotal",
		FilePath:  "src/math.ts",
		Language:  "typescript",
		Workspace: tempDir,
	}

	report, pack, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{sym},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	foundApp1 := false
	foundApp2 := false
	foundTest := false

	for _, ref := range report.ReferenceEdges {
		if ref.FilePath == "src/app1.ts" && ref.Relationship == "caller" {
			foundApp1 = true
		}
		if ref.FilePath == "src/app2.ts" && ref.Relationship == "caller" {
			foundApp2 = true
		}
		if ref.FilePath == "src/math.test.ts" && ref.Relationship == "test" {
			foundTest = true
		}
	}

	if !foundApp1 {
		t.Errorf("aliased import in app1.ts was not discovered through barrel re-export")
	}
	if !foundApp2 {
		t.Errorf("namespace import in app2.ts was not discovered")
	}
	if !foundTest {
		t.Errorf("test reference in math.test.ts was not discovered")
	}
	if len(pack.Items) == 0 {
		t.Errorf("expected packed items in context pack")
	}
}

// TestIssue02_TypeOnlyAndDynamicImports tests:
// "Type-only imports and dynamic imports carry distinct evidence and unresolved reasons."
func TestIssue02_TypeOnlyAndDynamicImports(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tempDir, "types.ts"), []byte(`export interface UserPayload {
	id: string;
	name: string;
}
`), 0644)

	// Type-only import in consumer
	_ = os.WriteFile(filepath.Join(tempDir, "consumer.ts"), []byte(`import type { UserPayload } from './types';

export function handleUser(user: UserPayload) {
	console.log(user.name);
}
`), 0644)

	// Computed dynamic import in dynamic.ts -> triggers incomplete discovery
	_ = os.WriteFile(filepath.Join(tempDir, "dynamic.ts"), []byte(`const mod = 'types';
const imported = require(mod);
`), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	sym := tzroctx.Symbol{
		Name:      "UserPayload",
		FilePath:  "types.ts",
		Language:  "typescript",
		Workspace: tempDir,
	}

	report, _, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{sym},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	foundTypeOnly := false
	for _, ref := range report.ReferenceEdges {
		if ref.FilePath == "consumer.ts" && ref.Relationship == "embedder" {
			foundTypeOnly = true
		}
	}

	if !foundTypeOnly {
		t.Errorf("type-only reference was not categorized as embedder in consumer.ts")
	}

	// Verify computed dynamic require produced an unresolved import warning
	if len(report.Coverage.UnresolvedImports) == 0 {
		t.Errorf("expected computed dynamic require to produce an unresolved import warning")
	}
	if !report.Coverage.IncompleteDiscovery {
		t.Errorf("expected IncompleteDiscovery=true due to computed dynamic import")
	}
}

// TestIssue02_NestedTSConfigResolution tests:
// "Nested packages use the correct configuration rather than only the repository-root aliases."
func TestIssue02_NestedTSConfigResolution(t *testing.T) {
	tempDir := t.TempDir()

	// Root tsconfig
	_ = os.WriteFile(filepath.Join(tempDir, "tsconfig.json"), []byte(`{
	"compilerOptions": {
		"baseUrl": ".",
		"paths": {
			"@/*": ["src/*"]
		}
	}
}`), 0644)

	// Sub-package packages/pkg-a with own tsconfig
	pkgDir := filepath.Join(tempDir, "packages", "pkg-a")
	_ = os.MkdirAll(filepath.Join(pkgDir, "src"), 0755)
	_ = os.WriteFile(filepath.Join(pkgDir, "tsconfig.json"), []byte(`{
	"compilerOptions": {
		"baseUrl": ".",
		"paths": {
			"@pkg-a/*": ["src/*"]
		}
	}
}`), 0644)

	// Target in packages/pkg-a/src/service.ts
	_ = os.WriteFile(filepath.Join(pkgDir, "src", "service.ts"), []byte(`export function executeService(): string {
	return "ok";
}
`), 0644)

	// Consumer in packages/pkg-a/src/client.ts using @pkg-a/* alias
	_ = os.WriteFile(filepath.Join(pkgDir, "src", "client.ts"), []byte(`import { executeService } from '@pkg-a/service';

export function run() {
	return executeService();
}
`), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	sym := tzroctx.Symbol{
		Name:      "executeService",
		FilePath:  "packages/pkg-a/src/service.ts",
		Language:  "typescript",
		Workspace: tempDir,
	}

	report, _, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{sym},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	foundNestedConsumer := false
	for _, ref := range report.ReferenceEdges {
		if ref.FilePath == filepath.Join("packages", "pkg-a", "src", "client.ts") && ref.Relationship == "caller" {
			foundNestedConsumer = true
		}
	}

	if !foundNestedConsumer {
		t.Errorf("nested tsconfig alias @pkg-a/* was not resolved in subpackage")
	}
}

// TestIssue02_MixedGoAndTSWorkspace tests:
// "Mixed Go/TS workspaces dispatch to both adapters without duplicate config references."
func TestIssue02_MixedGoAndTSWorkspace(t *testing.T) {
	tempDir := t.TempDir()

	// Go file
	_ = os.WriteFile(filepath.Join(tempDir, "api.go"), []byte(`package api
func HandleAPI() string { return "go" }
`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "server.go"), []byte(`package api
func RunServer() { HandleAPI() }
`), 0644)

	// TS file
	_ = os.WriteFile(filepath.Join(tempDir, "client.ts"), []byte(`export function requestAPI(): string {
	return "ts";
}
`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "app.ts"), []byte(`import { requestAPI } from './client';
export function main() { requestAPI(); }
`), 0644)

	// Shared config referencing both
	_ = os.WriteFile(filepath.Join(tempDir, "config.yaml"), []byte(`service:
  goHandler: api.HandleAPI
  tsClient: requestAPI
`), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	symGo := tzroctx.Symbol{
		Name:      "HandleAPI",
		FilePath:  "api.go",
		Language:  "go",
		Workspace: tempDir,
	}
	symTS := tzroctx.Symbol{
		Name:      "requestAPI",
		FilePath:  "client.ts",
		Language:  "typescript",
		Workspace: tempDir,
	}

	report, _, err := analyzer.AnalyzeReport(
		stdctx.Background(),
		tempDir,
		[]tzroctx.Symbol{symGo, symTS},
		4000,
		false,
		"working_tree",
	)
	if err != nil {
		t.Fatalf("AnalyzeReport failed: %v", err)
	}

	foundGoCall := false
	foundTSCall := false
	configHits := 0

	for _, ref := range report.ReferenceEdges {
		if ref.FilePath == "server.go" && ref.SymbolName == "HandleAPI" {
			foundGoCall = true
		}
		if ref.FilePath == "app.ts" && ref.SymbolName == "requestAPI" {
			foundTSCall = true
		}
		if ref.FilePath == "config.yaml" {
			configHits++
		}
	}

	if !foundGoCall {
		t.Errorf("Go caller in server.go was not found")
	}
	if !foundTSCall {
		t.Errorf("TS caller in app.ts was not found")
	}
	if configHits != 2 {
		t.Errorf("expected exactly 2 config references (one for Go, one for TS), got %d", configHits)
	}
}
