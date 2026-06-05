package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/config"
	"tzro/internal/inference"
)

// ModelSwapResult is the typed return value from activateModel.
type ModelSwapResult struct {
	Status       string `json:"status"` // "success" | "error" | "downloading"
	Message      string `json:"message,omitempty"`
	Error        string `json:"error,omitempty"`
	NewModelPath string `json:"newModelPath,omitempty"`
}

// IsError returns true if the swap result indicates a failure.
func (r ModelSwapResult) IsError() bool { return r.Status == "error" }

// ---------------------------------------------------------------------------
// tzro_model_list tool definition
// ---------------------------------------------------------------------------

// TzroModelListArgs defines the (empty) inputs for listing available models.
type TzroModelListArgs struct{}

func handleTzroModelList(ctx context.Context, req *mcp.CallToolRequest, args TzroModelListArgs) (*mcp.CallToolResult, any, error) {
	type ModelListEntry struct {
		inference.ModelEntry
		Downloaded bool   `json:"downloaded"`
		IsActive   bool   `json:"isActive"`
		LocalPath  string `json:"localPath,omitempty"`
	}

	catalog := inference.GetCatalog()
	modelsDir := config.GetModelsDir()
	cfg := config.Get()

	result := make([]ModelListEntry, 0, len(catalog))
	for _, entry := range catalog {
		downloaded := false
		modelPath := filepath.Join(modelsDir, entry.Filename)
		if _, err := os.Stat(modelPath); err == nil {
			downloaded = true
		}

		isActive := false
		if cfg.GGUFModelPath == modelPath {
			isActive = true
		}

		le := ModelListEntry{
			ModelEntry: entry,
			Downloaded: downloaded,
			IsActive:   isActive,
		}
		if downloaded {
			le.LocalPath = modelPath
		}
		result = append(result, le)
	}

	respMap := map[string]interface{}{
		"models":      result,
		"activeModel": cfg.GGUFModelPath,
		"modelsDir":   modelsDir,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// ---------------------------------------------------------------------------
// tzro_model_set tool definition
// ---------------------------------------------------------------------------

// TzroModelSetArgs defines the inputs for changing the active local worker model.
type TzroModelSetArgs struct {
	ModelID       string `json:"modelId,omitempty" jsonschema:"Catalog model ID to activate (e.g. qwen3-4b, phi4-mini). Use tzro_model_list to see available IDs."`
	GGUFModelPath string `json:"ggufModelPath,omitempty" jsonschema:"Direct absolute path to a GGUF model file already on disk."`
	DownloadURL   string `json:"downloadUrl,omitempty" jsonschema:"URL to download a GGUF model file from (e.g. HuggingFace). Download runs async."`
}

func handleTzroModelSet(ctx context.Context, req *mcp.CallToolRequest, args TzroModelSetArgs) (*mcp.CallToolResult, any, error) {
	// Validate: exactly one input mode
	modes := 0
	if args.ModelID != "" {
		modes++
	}
	if args.GGUFModelPath != "" {
		modes++
	}
	if args.DownloadURL != "" {
		modes++
	}
	if modes == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "must provide one of modelId, ggufModelPath, or downloadUrl"}`},
			},
			IsError: true,
		}, nil, nil
	}
	if modes > 1 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"error": "provide only one of modelId, ggufModelPath, or downloadUrl"}`},
			},
			IsError: true,
		}, nil, nil
	}

	modelsDir := config.GetModelsDir()
	cfg := config.Get()
	oldModelPath := cfg.GGUFModelPath

	var newModelPath string
	var downloadURL string
	var filename string

	// --- Mode 1: Catalog model ID ---
	if args.ModelID != "" {
		entry := inference.FindModelByID(args.ModelID)
		if entry == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "model not found in catalog: %s"}`, args.ModelID)},
				},
				IsError: true,
			}, nil, nil
		}

		candidatePath := filepath.Join(modelsDir, entry.Filename)
		if _, err := os.Stat(candidatePath); err == nil {
			// Already downloaded — activate immediately
			newModelPath = candidatePath
		} else {
			// Need to download first
			downloadURL = entry.DownloadURL
			filename = entry.Filename
		}
	}

	// --- Mode 2: Direct path ---
	if args.GGUFModelPath != "" {
		if _, err := os.Stat(args.GGUFModelPath); os.IsNotExist(err) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf(`{"error": "model file not found: %s"}`, args.GGUFModelPath)},
				},
				IsError: true,
			}, nil, nil
		}
		newModelPath = args.GGUFModelPath
	}

	// --- Mode 3: Download URL ---
	if args.DownloadURL != "" {
		downloadURL = args.DownloadURL
		parsed, err := url.Parse(args.DownloadURL)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: `{"error": "invalid download URL"}`},
				},
				IsError: true,
			}, nil, nil
		}
		filename = path.Base(parsed.Path)
		if filename == "" || filename == "." || filename == "/" {
			filename = "custom-model.gguf"
		}
	}

	// --- Synchronous activation (no download needed) ---
	if newModelPath != "" {
		result := activateModel(ctx, oldModelPath, newModelPath, modelsDir)
		respBytes, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
			IsError: result.IsError(),
		}, nil, nil
	}

	// --- Async download path ---
	finalPath := filepath.Join(modelsDir, filename)

	// If file already exists, just activate it
	if _, err := os.Stat(finalPath); err == nil {
		result := activateModel(ctx, oldModelPath, finalPath, modelsDir)
		respBytes, _ := json.MarshalIndent(result, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(respBytes)},
			},
			IsError: result.IsError(),
		}, nil, nil
	}

	inference.GlobalLocalModel.ManifestProgress = 0

	go func() {
		tmpPath := filepath.Join(modelsDir, filename+".downloading")

		httpReq, err := http.NewRequest("GET", downloadURL, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Model Download] Failed to create request: %v\n", err)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}
		httpReq.Header.Set("User-Agent", "tzro-engine/1.0")

		client := &http.Client{}
		resp, err := client.Do(httpReq)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Model Download] HTTP request failed: %v\n", err)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "[Model Download] Server returned status %s\n", resp.Status)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		totalSize := resp.ContentLength

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Model Download] Failed to create temp file: %v\n", err)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		buf := make([]byte, 32*1024)
		var downloaded int64

		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := tmpFile.Write(buf[:n])
				if writeErr != nil {
					fmt.Fprintf(os.Stderr, "[Model Download] Write error: %v\n", writeErr)
					tmpFile.Close()
					_ = os.Remove(tmpPath)
					inference.GlobalLocalModel.ManifestProgress = 0
					return
				}
				downloaded += int64(n)
				if totalSize > 0 {
					pct := int(downloaded * 99 / totalSize)
					if pct > 99 {
						pct = 99
					}
					inference.GlobalLocalModel.ManifestProgress = pct
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				fmt.Fprintf(os.Stderr, "[Model Download] Read error: %v\n", readErr)
				tmpFile.Close()
				_ = os.Remove(tmpPath)
				inference.GlobalLocalModel.ManifestProgress = 0
				return
			}
		}
		tmpFile.Close()

		if err := os.Rename(tmpPath, finalPath); err != nil {
			fmt.Fprintf(os.Stderr, "[Model Download] Failed to rename temp file: %v\n", err)
			_ = os.Remove(tmpPath)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		inference.GlobalLocalModel.ManifestProgress = 100
		fmt.Fprintf(os.Stderr, "[Model Download] Successfully downloaded %s\n", filename)

		// Auto-activate the downloaded model
		activateModel(context.Background(), oldModelPath, finalPath, modelsDir)
	}()

	respMap := map[string]interface{}{
		"status":      "downloading",
		"message":     fmt.Sprintf("Model download started for %s. Use tzro_status to monitor sidecar readiness.", filename),
		"downloadUrl": downloadURL,
		"targetPath":  finalPath,
	}
	respBytes, _ := json.MarshalIndent(respMap, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(respBytes)},
		},
	}, nil, nil
}

// activateModel performs the model swap lifecycle: cleanup old model, update config, restart sidecar.
func activateModel(ctx context.Context, oldModelPath, newModelPath, modelsDir string) ModelSwapResult {
	// 1. Cleanup old model if it lives inside the managed .tzro models directory
	if oldModelPath != "" && oldModelPath != newModelPath {
		absOld, err1 := filepath.Abs(oldModelPath)
		absModels, err2 := filepath.Abs(modelsDir)
		if err1 == nil && err2 == nil && strings.HasPrefix(absOld, absModels+string(filepath.Separator)) {
			if _, err := os.Stat(absOld); err == nil {
				if err := os.Remove(absOld); err != nil {
					fmt.Fprintf(os.Stderr, "[Model Swap] Warning: failed to delete old model %s: %v\n", absOld, err)
				} else {
					fmt.Fprintf(os.Stderr, "[Model Swap] Deleted old managed model: %s\n", absOld)
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "[Model Swap] Keeping old model (not in managed dir): %s\n", oldModelPath)
		}
	}

	// 2. Update config
	cfg := config.Get()
	cfg.GGUFModelPath = newModelPath
	if err := config.Save(&cfg); err != nil {
		return ModelSwapResult{
			Status: "error",
			Error:  fmt.Sprintf("failed to save config: %v", err),
		}
	}

	// 3. Stop current sidecar
	_ = inference.StopActive()

	// 4. Start sidecar with new model
	startErr := inference.StartActive(ctx)

	if startErr != nil {
		return ModelSwapResult{
			Status:       "error",
			Error:        fmt.Sprintf("model config updated but sidecar failed to start: %v", startErr),
			NewModelPath: newModelPath,
		}
	}

	return ModelSwapResult{
		Status:       "success",
		Message:      "Model swapped and sidecar restarted successfully.",
		NewModelPath: newModelPath,
	}
}
