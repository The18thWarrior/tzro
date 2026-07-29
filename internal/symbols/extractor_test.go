package symbols

import (
	"testing"
)

// --- Behavior 1: Go extraction ---

func TestExtractSymbols_Go_BasicDeclarations(t *testing.T) {
	src := []byte(`package inference

import "context"

// InferenceBackend defines the interface for LLM inference providers.
type InferenceBackend interface {
	CallModel(ctx context.Context, messages []Message, schema string) (*Result, error)
}

// LlamaServerBackend is a concrete backend using llama-server.
type LlamaServerBackend struct {
	URL     string
	ModelID string
}

// CallModel implements InferenceBackend.
func (b *LlamaServerBackend) CallModel(ctx context.Context, messages []Message, schema string) (*Result, error) {
	return nil, nil
}

// NewLlamaBackend creates a new LlamaServerBackend.
func NewLlamaBackend(url string) *LlamaServerBackend {
	return &LlamaServerBackend{URL: url}
}

// MaxTokens is the default maximum token count.
const MaxTokens = 4096

// DefaultModel is the active model identifier.
var DefaultModel = "qwen-4b"

// internal helper — should NOT be extracted
func sanitizePrompt(s string) string {
	return s
}

// unexported type — should NOT be extracted
type internalCache struct{}
`)

	symbols, err := ExtractSymbols("backend.go", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	// Build a name→symbol map for assertions
	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	// Should extract exported declarations
	expectedNames := []string{
		"InferenceBackend",
		"LlamaServerBackend",
		"CallModel",
		"NewLlamaBackend",
		"MaxTokens",
		"DefaultModel",
	}
	for _, name := range expectedNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("expected symbol %q not found; got symbols: %v", name, symbolNames(symbols))
		}
	}

	// Should NOT extract unexported declarations
	unexpectedNames := []string{"sanitizePrompt", "internalCache"}
	for _, name := range unexpectedNames {
		if _, ok := byName[name]; ok {
			t.Errorf("unexported symbol %q should not be extracted", name)
		}
	}

	// Verify kinds
	assertKind(t, byName, "InferenceBackend", SymbolInterface)
	assertKind(t, byName, "LlamaServerBackend", SymbolType)
	assertKind(t, byName, "NewLlamaBackend", SymbolFunc)
	assertKind(t, byName, "CallModel", SymbolMethod)
	assertKind(t, byName, "MaxTokens", SymbolConst)
	assertKind(t, byName, "DefaultModel", SymbolVar)

	// Verify line numbers are positive (basic sanity)
	for _, s := range symbols {
		if s.Line <= 0 {
			t.Errorf("symbol %q has non-positive line number: %d", s.Name, s.Line)
		}
	}

	// Verify all have File set
	for _, s := range symbols {
		if s.File != "backend.go" {
			t.Errorf("symbol %q has wrong file: %q", s.Name, s.File)
		}
	}

	// Verify signatures contain the symbol name
	for _, s := range symbols {
		if s.Signature == "" {
			t.Errorf("symbol %q has empty signature", s.Name)
		}
	}
}

// --- Behavior 2: Multi-line Go signatures ---

func TestExtractSymbols_Go_MultiLineSignature(t *testing.T) {
	src := []byte(`package inference

func ExecuteStructured(
	ctx context.Context,
	messages []InferenceMessage,
	jsonSchema string,
) (*InferenceResult, error) {
	return nil, nil
}
`)

	symbols, err := ExtractSymbols("multi.go", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d: %v", len(symbols), symbolNames(symbols))
	}

	sym := symbols[0]
	if sym.Name != "ExecuteStructured" {
		t.Errorf("expected name ExecuteStructured, got %q", sym.Name)
	}

	// The signature should include parameters from all lines
	if sym.Signature == "" {
		t.Error("signature is empty for multi-line function")
	}
	// Should contain parameter names from continuation lines
	for _, param := range []string{"ctx", "messages", "jsonSchema"} {
		if !contains(sym.Signature, param) {
			t.Errorf("signature %q missing parameter %q", sym.Signature, param)
		}
	}
}

// --- Behavior 3: Python extraction ---

func TestExtractSymbols_Python(t *testing.T) {
	src := []byte(`
class ModelRouter:
    """Routes inference requests."""
    def __init__(self, config):
        self.config = config

    def route(self, prompt):
        pass

def create_router(config):
    return ModelRouter(config)

def _internal_helper():
    pass

class _PrivateClass:
    pass
`)

	symbols, err := ExtractSymbols("router.py", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	// Should extract public class and function
	if _, ok := byName["ModelRouter"]; !ok {
		t.Errorf("expected ModelRouter class; got: %v", symbolNames(symbols))
	}
	if _, ok := byName["create_router"]; !ok {
		t.Errorf("expected create_router function; got: %v", symbolNames(symbols))
	}

	// Should NOT extract _private
	if _, ok := byName["_internal_helper"]; ok {
		t.Error("_internal_helper should not be extracted")
	}
	if _, ok := byName["_PrivateClass"]; ok {
		t.Error("_PrivateClass should not be extracted")
	}

	// Verify kinds
	assertKind(t, byName, "ModelRouter", SymbolClass)
	assertKind(t, byName, "create_router", SymbolFunc)
}

// --- Behavior 4: TypeScript extraction ---

func TestExtractSymbols_TypeScript(t *testing.T) {
	src := []byte(`
export function parseConfig(raw: string): Config {
  return JSON.parse(raw);
}

export class EventBus {
  emit(event: string): void {}
}

export interface Plugin {
  name: string;
  init(): void;
}

export type Result<T> = { ok: true; value: T } | { ok: false; error: Error };

export const DEFAULT_TIMEOUT = 5000;

// Non-exported — should NOT appear
function internalHelper() {}
class InternalClass {}
`)

	symbols, err := ExtractSymbols("config.ts", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	expected := []string{"parseConfig", "EventBus", "Plugin", "Result", "DEFAULT_TIMEOUT"}
	for _, name := range expected {
		if _, ok := byName[name]; !ok {
			t.Errorf("expected symbol %q not found; got: %v", name, symbolNames(symbols))
		}
	}

	// Non-exported should not appear
	if _, ok := byName["internalHelper"]; ok {
		t.Error("internalHelper should not be extracted")
	}
	if _, ok := byName["InternalClass"]; ok {
		t.Error("InternalClass should not be extracted")
	}

	// Verify kinds
	assertKind(t, byName, "parseConfig", SymbolFunc)
	assertKind(t, byName, "EventBus", SymbolClass)
	assertKind(t, byName, "Plugin", SymbolInterface)
	assertKind(t, byName, "Result", SymbolType)
	assertKind(t, byName, "DEFAULT_TIMEOUT", SymbolConst)
}

// --- Behavior 5: Rust extraction ---

func TestExtractSymbols_Rust(t *testing.T) {
	src := []byte(`
pub fn create_engine(config: Config) -> Engine {
    Engine::new(config)
}

pub struct Engine {
    config: Config,
}

pub trait Backend {
    fn execute(&self) -> Result<()>;
}

pub enum Status {
    Running,
    Stopped,
}

fn private_helper() {}

struct InternalState {}
`)

	symbols, err := ExtractSymbols("engine.rs", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	expected := []string{"create_engine", "Engine", "Backend", "Status"}
	for _, name := range expected {
		if _, ok := byName[name]; !ok {
			t.Errorf("expected symbol %q not found; got: %v", name, symbolNames(symbols))
		}
	}

	// Private items should not appear
	if _, ok := byName["private_helper"]; ok {
		t.Error("private_helper should not be extracted")
	}
	if _, ok := byName["InternalState"]; ok {
		t.Error("InternalState should not be extracted")
	}

	assertKind(t, byName, "create_engine", SymbolFunc)
	assertKind(t, byName, "Engine", SymbolType)
	assertKind(t, byName, "Backend", SymbolTrait)
	assertKind(t, byName, "Status", SymbolEnum)
}

// --- Behavior 6: Java extraction ---

func TestExtractSymbols_Java(t *testing.T) {
	src := []byte(`
public class UserService {
    public String getName() {
        return "test";
    }

    private void internalMethod() {}
}

public interface Repository {
    void save(Object entity);
}
`)

	symbols, err := ExtractSymbols("UserService.java", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	if _, ok := byName["UserService"]; !ok {
		t.Errorf("expected UserService; got: %v", symbolNames(symbols))
	}
	if _, ok := byName["getName"]; !ok {
		t.Errorf("expected getName; got: %v", symbolNames(symbols))
	}
	if _, ok := byName["Repository"]; !ok {
		t.Errorf("expected Repository; got: %v", symbolNames(symbols))
	}

	// Private method should not appear
	if _, ok := byName["internalMethod"]; ok {
		t.Error("internalMethod should not be extracted")
	}

	assertKind(t, byName, "UserService", SymbolClass)
	assertKind(t, byName, "Repository", SymbolInterface)
}

// --- Behavior 7: Unknown language graceful degradation ---

func TestExtractSymbols_UnknownLanguage(t *testing.T) {
	src := []byte("some content in an unknown format")
	symbols, err := ExtractSymbols("data.xyz", src)
	if err != nil {
		t.Fatalf("expected no error for unknown language, got: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected empty symbols for unknown language, got: %v", symbolNames(symbols))
	}
}

// --- Behavior 8: Empty file ---

func TestExtractSymbols_EmptyFile(t *testing.T) {
	symbols, err := ExtractSymbols("empty.go", []byte{})
	if err != nil {
		t.Fatalf("expected no error for empty file, got: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected empty symbols for empty file, got %d", len(symbols))
	}
}

// --- Test Helpers ---

func symbolNames(symbols []Symbol) []string {
	names := make([]string, len(symbols))
	for i, s := range symbols {
		names[i] = s.Name
	}
	return names
}

func assertKind(t *testing.T, byName map[string]Symbol, name string, expected SymbolKind) {
	t.Helper()
	if sym, ok := byName[name]; ok {
		if sym.Kind != expected {
			t.Errorf("symbol %q: expected kind %q, got %q", name, expected, sym.Kind)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Slice 1: DocComment and EndLine ---

func TestExtractSymbols_Go_DocComment(t *testing.T) {
	src := []byte(`package example

// RunServer starts the HTTP server.
func RunServer(addr string) error {
	return nil
}

// Config holds application settings.
type Config struct {
	Port int
}

// comment that is NOT a doc comment (blank line separates it)

func HandleRequest() {}

// ProcessData transforms input records.
// It applies the configured pipeline.
func ProcessData(input []byte) []byte {
	return input
}
`)

	symbols, err := ExtractSymbols("example.go", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	// RunServer should have doc comment
	if sym, ok := byName["RunServer"]; !ok {
		t.Fatal("RunServer not found")
	} else {
		if sym.DocComment != "RunServer starts the HTTP server." {
			t.Errorf("RunServer.DocComment = %q, want %q", sym.DocComment, "RunServer starts the HTTP server.")
		}
	}

	// Config should have doc comment
	if sym, ok := byName["Config"]; !ok {
		t.Fatal("Config not found")
	} else {
		if sym.DocComment != "Config holds application settings." {
			t.Errorf("Config.DocComment = %q, want %q", sym.DocComment, "Config holds application settings.")
		}
	}

	// HandleRequest has no doc comment (blank line separates it)
	if sym, ok := byName["HandleRequest"]; !ok {
		t.Fatal("HandleRequest not found")
	} else {
		if sym.DocComment != "" {
			t.Errorf("HandleRequest.DocComment = %q, want empty", sym.DocComment)
		}
	}

	// ProcessData has multi-line doc — we capture first line only
	if sym, ok := byName["ProcessData"]; !ok {
		t.Fatal("ProcessData not found")
	} else {
		if sym.DocComment != "ProcessData transforms input records." {
			t.Errorf("ProcessData.DocComment = %q, want %q", sym.DocComment, "ProcessData transforms input records.")
		}
	}
}

func TestExtractSymbols_Go_EndLine(t *testing.T) {
	src := []byte(`package example

func Short() {}

func Multi(
	a int,
	b int,
) error {
	if a > b {
		return nil
	}
	return nil
}
`)

	symbols, err := ExtractSymbols("example.go", src)
	if err != nil {
		t.Fatalf("ExtractSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	// Short is a one-liner on line 3
	if sym, ok := byName["Short"]; !ok {
		t.Fatal("Short not found")
	} else {
		if sym.EndLine <= 0 {
			t.Errorf("Short.EndLine = %d, want positive", sym.EndLine)
		}
		if sym.EndLine < sym.Line {
			t.Errorf("Short.EndLine (%d) < Short.Line (%d)", sym.EndLine, sym.Line)
		}
	}

	// Multi spans multiple lines
	if sym, ok := byName["Multi"]; !ok {
		t.Fatal("Multi not found")
	} else {
		if sym.EndLine <= sym.Line {
			t.Errorf("Multi.EndLine (%d) should be > Multi.Line (%d)", sym.EndLine, sym.Line)
		}
	}
}

// --- Slice 2: ExtractAllSymbols ---

func TestExtractAllSymbols_Go_IncludesUnexported(t *testing.T) {
	src := []byte(`package example

// RunServer starts the HTTP server.
func RunServer(addr string) error {
	return nil
}

func helperFunc() string {
	return "internal"
}

type Config struct {
	Port int
}

type internalState struct {
	active bool
}
`)

	symbols, err := ExtractAllSymbols("example.go", src)
	if err != nil {
		t.Fatalf("ExtractAllSymbols returned error: %v", err)
	}

	byName := make(map[string]Symbol)
	for _, s := range symbols {
		byName[s.Name] = s
	}

	// Should include exported
	if _, ok := byName["RunServer"]; !ok {
		t.Error("expected exported RunServer")
	}
	if _, ok := byName["Config"]; !ok {
		t.Error("expected exported Config")
	}

	// Should also include unexported
	if _, ok := byName["helperFunc"]; !ok {
		t.Error("expected unexported helperFunc")
	}
	if _, ok := byName["internalState"]; !ok {
		t.Error("expected unexported internalState")
	}

	// Exported field should be set correctly
	if sym := byName["RunServer"]; !sym.Exported {
		t.Error("RunServer should have Exported=true")
	}
	if sym := byName["helperFunc"]; sym.Exported {
		t.Error("helperFunc should have Exported=false")
	}
}
