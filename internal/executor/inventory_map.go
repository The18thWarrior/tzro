package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tzro/internal/compactor"
	"tzro/internal/tools"
)

// InventoryRow holds a single extracted file record for the Inventory Matrix.
type InventoryRow struct {
	File     string            `json:"file"`
	Relevant bool              `json:"relevant"`
	Fields   map[string]string `json:"fields"`
}

var codeExtensions = map[string]bool{
	".go":   true,
	".py":   true,
	".ts":   true,
	".tsx":  true,
	".js":   true,
	".jsx":  true,
	".rs":   true,
	".cpp":  true,
	".c":    true,
	".h":    true,
	".java": true,
	".cs":   true,
	".rb":   true,
}

// SliceContentForMap applies content-aware slicing to keep the Map phase fast (<0.5s)
// without blowing local inference context windows.
func SliceContentForMap(filePath string, rawContent string) string {
	lines := strings.Split(rawContent, "\n")
	if len(lines) <= 200 {
		return rawContent
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if codeExtensions[ext] {
		// Use AST / heuristic code skeleton
		skeleton := compactor.ExtractSkeleton(rawContent, 0)
		if skeleton != "" {
			return skeleton
		}
	}

	// For Markdown, text, and other documents: return the top 150 lines
	if len(lines) > 150 {
		return strings.Join(lines[:150], "\n") + "\n\n[... truncated for inventory map ...]"
	}

	return rawContent
}

// ExtractFileInventory runs 1-shot GBNF extraction on a single file.
// Injects the file path deterministically and returns nil if the file is irrelevant.
func ExtractFileInventory(
	ctx context.Context,
	filePath string,
	rawContent string,
	schema *InventorySchema,
	engine ProbeInferenceEngine,
) (*InventoryRow, error) {
	if schema == nil || engine == nil {
		return nil, fmt.Errorf("schema and engine must not be nil")
	}

	slicedContent := SliceContentForMap(filePath, rawContent)

	systemPrompt := "You are a fast, precise file inventory extractor.\n" +
		"Read the provided file content and extract the structured fields according to the goal and schema.\n" +
		"If the file content contains NO useful information for the goal, return {\"relevant\": false}.\n" +
		"Otherwise, set relevant: true and extract all fields concisely."

	userPrompt := fmt.Sprintf("File: %s\n\nContent:\n%s", filePath, slicedContent)

	resp, err := engine.Infer(ctx, systemPrompt, userPrompt, schema.Grammar, TargetWorker)
	if err != nil {
		return nil, fmt.Errorf("inventory extraction failed for %s: %w", filePath, err)
	}

	cleaned := stripThoughtAndFences(resp)
	var rawMap map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(cleaned), &rawMap); jsonErr != nil {
		// Fallback: search between outermost braces
		firstBrace := strings.Index(cleaned, "{")
		lastBrace := strings.LastIndex(cleaned, "}")
		if firstBrace >= 0 && lastBrace > firstBrace {
			_ = json.Unmarshal([]byte(cleaned[firstBrace:lastBrace+1]), &rawMap)
		}
	}

	if rawMap == nil {
		return nil, fmt.Errorf("failed to parse JSON extraction for %s", filePath)
	}

	// Check relevance escape
	if rel, ok := rawMap["relevant"].(bool); ok && !rel {
		return nil, nil // Skips irrelevant file
	}

	extractedFields := make(map[string]string)
	for _, f := range schema.Fields {
		if val, exists := rawMap[f.Name]; exists {
			switch v := val.(type) {
			case string:
				extractedFields[f.Name] = v
			case []interface{}:
				items := make([]string, 0, len(v))
				for _, item := range v {
					items = append(items, fmt.Sprintf("%v", item))
				}
				extractedFields[f.Name] = strings.Join(items, ", ")
			default:
				extractedFields[f.Name] = fmt.Sprintf("%v", v)
			}
		}
	}

	return &InventoryRow{
		File:     filePath,
		Relevant: true,
		Fields:   extractedFields,
	}, nil
}

// InventoryMapDriver executes the Map phase across all candidate files.
type InventoryMapDriver struct {
	Files   []string
	Schema  *InventorySchema
	Engine  ProbeInferenceEngine
	Results []InventoryRow
}

// Execute drains candidate files, extracts GBNF rows, and produces the Inventory Matrix.
func (d *InventoryMapDriver) Execute(
	ctx context.Context,
	phase *Phase,
	runnerCtx *PhaseRunnerContext,
) (*StageExecutionResult, error) {
	result := &StageExecutionResult{}
	if len(d.Files) == 0 {
		return result, nil
	}

	engine := d.Engine
	if engine == nil && runnerCtx != nil {
		engine = runnerCtx.Engine
	}

	for _, file := range d.Files {
		var content string
		var err error
		if runnerCtx != nil && runnerCtx.ToolDispatcher != nil {
			content, err = runnerCtx.ToolDispatcher(ctx, "read_file", map[string]interface{}{"path": file})
		} else {
			content, err = tools.Call(ctx, "read_file", map[string]interface{}{"path": file})
		}

		if err != nil || content == "" {
			data, readErr := os.ReadFile(file)
			if readErr != nil {
				continue
			}
			content = string(data)
		}

		result.StepsUsed++
		result.ToolsCalled = append(result.ToolsCalled, "read_file")
		if runnerCtx != nil && runnerCtx.SourceTracker != nil {
			runnerCtx.SourceTracker.AddFileSource(file, nil, strings.Count(content, "\n")+1, "")
		}

		row, extractErr := ExtractFileInventory(ctx, file, content, d.Schema, engine)
		if extractErr == nil && row != nil && row.Relevant {
			d.Results = append(d.Results, *row)
		}
	}

	result.LastOutput = FormatInventoryMatrix(d.Results)
	result.ToolOutputLog = []string{result.LastOutput}
	return result, nil
}

// DynamicSchemaDriver derives the extraction schema during the derive_schema phase.
type DynamicSchemaDriver struct {
	Goal            string
	Engine          ProbeInferenceEngine
	OnSchemaDerived func(schema *InventorySchema)
}

// Execute derives the schema and invokes the callback.
func (d *DynamicSchemaDriver) Execute(
	ctx context.Context,
	phase *Phase,
	runnerCtx *PhaseRunnerContext,
) (*StageExecutionResult, error) {
	schema, err := DeriveInventorySchema(ctx, d.Goal, d.Engine)
	if err != nil || schema == nil {
		schema = &InventorySchema{
			Fields:  defaultUniversalFields,
			Grammar: CompileInventoryGBNF(defaultUniversalFields),
		}
	}
	if d.OnSchemaDerived != nil {
		d.OnSchemaDerived(schema)
	}
	return &StageExecutionResult{
		StepsUsed:  1,
		LastOutput: fmt.Sprintf("Derived %d extraction fields", len(schema.Fields)),
	}, nil
}


