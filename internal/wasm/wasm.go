package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WasmToolAdapter wraps a compiled WebAssembly binary to satisfy the Tool interface.
type WasmToolAdapter struct {
	name       string
	wasmPath   string
	schemaPath string
	timeout    time.Duration
}

// Option configures a WasmToolAdapter instance.
type Option func(*WasmToolAdapter)

// WithTimeout sets a custom execution timeout.
func WithTimeout(d time.Duration) Option {
	return func(w *WasmToolAdapter) {
		w.timeout = d
	}
}

// NewWasmToolAdapter instantiates a new adapter with standard options.
func NewWasmToolAdapter(name string, wasmPath string, schemaPath string, opts ...Option) *WasmToolAdapter {
	w := &WasmToolAdapter{
		name:       name,
		wasmPath:   wasmPath,
		schemaPath: schemaPath,
		timeout:    30 * time.Second, // default generous 30 seconds
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Name returns the unique tool identifier.
func (w *WasmToolAdapter) Name() string {
	return w.name
}

// Description returns an empty string; WASM tools don't carry descriptions.
func (w *WasmToolAdapter) Description() string {
	return ""
}

// GetSchema returns the GBNF-enforcing JSON schema.
func (w *WasmToolAdapter) GetSchema() (string, error) {
	if w.schemaPath == "" {
		return "", fmt.Errorf("no schema path configured for WASM tool '%s'", w.name)
	}
	schemaBytes, err := os.ReadFile(w.schemaPath)
	if err != nil {
		return "", fmt.Errorf("failed to read schema file: %w", err)
	}
	return string(schemaBytes), nil
}

// Call compiles, instantiates, and runs the WASM binary using wazero.
func (w *WasmToolAdapter) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	// 1. Check if WASM binary exists
	wasmBytes, err := os.ReadFile(w.wasmPath)
	if err != nil {
		return "", fmt.Errorf("failed to read WASM binary: %w", err)
	}

	// 2. Set up context with timeout/cancellation
	execCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	// 3. Serialize inputs to JSON
	inputJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("failed to marshal input arguments: %w", err)
	}

	// 4. Set up buffers for stdout, stderr, and stdin
	var stdoutBuf, stderrBuf bytes.Buffer
	stdinReader := bytes.NewReader(inputJSON)

	// 5. Create wazero runtime with context timeout support
	rtCfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	r := wazero.NewRuntimeWithConfig(execCtx, rtCfg)
	defer r.Close(execCtx)

	// 6. Instantiate WASI snapshot preview1
	wasi_snapshot_preview1.MustInstantiate(execCtx, r)

	// 7. Configure WASI sandbox options:
	// - Pass JSON input to stdin
	// - Capture stdout and stderr
	// - NO environment variables
	// - NO filesystem pre-opens (completely sandboxed)
	config := wazero.NewModuleConfig().
		WithStdin(stdinReader).
		WithStdout(&stdoutBuf).
		WithStderr(&stderrBuf)

	// 8. Compile and instantiate the module (which triggers main execution automatically in WASIP1)
	mod, err := r.InstantiateWithConfig(execCtx, wasmBytes, config)
	if err != nil {
		stderrStr := strings.TrimSpace(stderrBuf.String())
		if stderrStr != "" {
			return "", fmt.Errorf("wasm execution error: %w (stderr: %s)", err, stderrStr)
		}
		return "", fmt.Errorf("failed to run WASM module: %w", err)
	}
	defer mod.Close(execCtx)

	// 9. Return stdout output
	return stdoutBuf.String(), nil
}
