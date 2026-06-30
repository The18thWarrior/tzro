package codegen

// DiffHunk represents a single edit operation within a file.
type DiffHunk struct {
	// SearchContent is the exact substring to locate in the existing file.
	// Must match a unique location. Include enough surrounding lines for uniqueness.
	SearchContent string `json:"searchContent"`

	// ReplaceContent is the content to substitute for SearchContent.
	// Empty string means deletion.
	ReplaceContent string `json:"replaceContent"`

	// Description is a brief explanation of what this hunk does (for logging).
	Description string `json:"description,omitempty"`
}

// DiffOutput is the structured output from the reason_code node in diff mode.
type DiffOutput struct {
	Hunks []DiffHunk `json:"hunks"`
}

// DiffHunkSchema is the JSON schema for GBNF grammar constraint on diff output.
const DiffHunkSchema = `{
  "type": "object",
  "properties": {
    "hunks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "searchContent": { "type": "string" },
          "replaceContent": { "type": "string" },
          "description": { "type": "string" }
        },
        "required": ["searchContent", "replaceContent"]
      }
    }
  },
  "required": ["hunks"]
}`
