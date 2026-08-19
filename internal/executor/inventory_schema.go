package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tzro/internal/config"
)

// InventoryField defines a single extraction attribute for the Map phase.
type InventoryField struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MinLength   int    `json:"minLength,omitempty"`
	MaxLength   int    `json:"maxLength,omitempty"`
}

// InventorySchema holds the derived fields and the compiled GBNF grammar.
type InventorySchema struct {
	Fields  []InventoryField `json:"fields"`
	Grammar string           `json:"grammar"`
}

var defaultUniversalFields = []InventoryField{
	{Name: "title_or_symbol", Description: "Document title, package, or primary component name", MinLength: 3, MaxLength: 128},
	{Name: "type_or_status", Description: "Status (e.g. Accepted/Draft) or component category/language", MinLength: 2, MaxLength: 64},
	{Name: "summary", Description: "Core takeaway, decision, or architectural purpose", MinLength: 10, MaxLength: 256},
	{Name: "exported_identifiers", Description: "Key exported functions, types, routes, or references", MinLength: 2, MaxLength: 256},
}

// CompileInventoryGBNF programmatically builds a GBNF grammar enforcing the schema fields
// and providing the fast-escape `{"relevant": false}` branch for uninformative files.
func CompileInventoryGBNF(fields []InventoryField) string {
	maxLenCap := config.GetMaxFieldLength()
	if maxLenCap <= 0 {
		maxLenCap = 256
	}

	var b strings.Builder
	b.WriteString("root ::= (relevant_object | irrelevant_object)\n")
	b.WriteString(`irrelevant_object ::= "{" ws "\"relevant\":" ws "false" ws "}"` + "\n")
	b.WriteString(`relevant_object ::= "{" ws "\"relevant\":" ws "true" ws "," ws `)

	fieldRules := make([]string, 0, len(fields))
	for i, f := range fields {
		ruleName := fmt.Sprintf("field_%s", sanitizeGrammarName(f.Name))
		if i == 0 {
			b.WriteString(fmt.Sprintf(`"\"%s\":" ws %s`, f.Name, ruleName))
		} else {
			b.WriteString(fmt.Sprintf(` ws "," ws "\"%s\":" ws %s`, f.Name, ruleName))
		}
		fieldRules = append(fieldRules, ruleName)
	}
	b.WriteString(` ws "}"` + "\n\n")

	// Emit string rules for each field
	for i, f := range fields {
		ruleName := fieldRules[i]
		minLen := f.MinLength
		if minLen <= 0 {
			minLen = 1
		}
		maxLen := f.MaxLength
		if maxLen <= 0 || maxLen > maxLenCap {
			maxLen = maxLenCap
		}
		if minLen > maxLen {
			minLen = maxLen
		}

		b.WriteString(fmt.Sprintf(`%s ::= "\"" [^\n\"]{%d,%d} "\""`+"\n", ruleName, minLen, maxLen))
	}

	b.WriteString("\nws ::= [ \\t\\n\\r]*\n")
	return b.String()
}

func sanitizeGrammarName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	res := b.String()
	if res == "" {
		return "field"
	}
	return res
}

const schemaDerivationGBNF = `root ::= "{" ws "\"fields\":" ws "[" ws field_item (ws "," ws field_item)* ws "]" ws "}"
field_item ::= "{" ws "\"name\":" ws string_value ws "," ws "\"description\":" ws string_value ws ("," ws "\"minLength\":" ws integer_value)? ws ("," ws "\"maxLength\":" ws integer_value)? ws "}"
string_value ::= "\"" [^\n\"]{2,64} "\""
integer_value ::= [0-9]{1,4}
ws ::= [ \t\n\r]*
`

// DeriveInventorySchema calls the local model to derive 3-6 task-specific extraction fields,
// bounding the results and compiling a live GBNF grammar.
func DeriveInventorySchema(ctx context.Context, goal string, engine ProbeInferenceEngine) (*InventorySchema, error) {
	minFields := config.GetMinExtractionFields()
	maxFields := config.GetMaxExtractionFields()
	maxFieldLen := config.GetMaxFieldLength()

	systemPrompt := fmt.Sprintf(
		"You are a structured schema planner. Given a documentation or repository synthesis goal, derive between %d and %d key extraction fields needed from each source file to answer the goal.\n"+
			"Output valid JSON with field names, short descriptions, and optional minLength/maxLength (max %d chars per field).",
		minFields, maxFields, maxFieldLen,
	)

	userPrompt := fmt.Sprintf("Goal: %s\nPlan the extraction schema in JSON now.", goal)

	var resp string
	var err error
	if engine != nil {
		resp, err = engine.Infer(ctx, systemPrompt, userPrompt, schemaDerivationGBNF, TargetWorker)
	}

	if err == nil && resp != "" {
		cleaned := stripThoughtAndFences(resp)
		var parsed struct {
			Fields []InventoryField `json:"fields"`
		}
		if jsonErr := json.Unmarshal([]byte(cleaned), &parsed); jsonErr == nil && len(parsed.Fields) >= minFields {
			fields := parsed.Fields
			if len(fields) > maxFields {
				fields = fields[:maxFields]
			}
			// Sanitize lengths
			for i := range fields {
				if fields[i].MinLength <= 0 {
					fields[i].MinLength = 3
				}
				if fields[i].MaxLength <= 0 || fields[i].MaxLength > maxFieldLen {
					fields[i].MaxLength = maxFieldLen
				}
			}
			return &InventorySchema{
				Fields:  fields,
				Grammar: CompileInventoryGBNF(fields),
			}, nil
		}
	}

	// Fallback to default universal schema
	return &InventorySchema{
		Fields:  defaultUniversalFields,
		Grammar: CompileInventoryGBNF(defaultUniversalFields),
	}, nil
}
