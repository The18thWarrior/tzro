package inference

// ModelEntry represents a downloadable GGUF model in the catalog.
type ModelEntry struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	Params       string `json:"params"`
	SizeBytes    int64  `json:"sizeBytes"`
	SizeLabel    string `json:"sizeLabel"`
	DownloadURL  string `json:"downloadUrl"`
	Filename     string `json:"filename"`
	Description  string `json:"description"`
	ToolCallTier string `json:"toolCallTier"`
	IsDefault    bool   `json:"isDefault"`
}

var modelCatalog = []ModelEntry{
	{
		ID:           "gemma-4-12b-it-qat",
		DisplayName:  "Gemma 4 12B IT QAT",
		Params:       "12B",
		SizeBytes:    6975877728,
		SizeLabel:    "~6.5 GB",
		DownloadURL:  "https://huggingface.co/google/gemma-4-12B-it-qat-q4_0-gguf/resolve/main/gemma-4-12b-it-qat-q4_0.gguf?download=true",
		Filename:     "gemma-4-12b-it-qat-q4_0.gguf",
		Description:  "Default Gemma 4 model with QAT calibration",
		ToolCallTier: "excellent",
		IsDefault:    true,
	},
	{
		ID:           "grm-25",
		DisplayName:  "GRM 2.5",
		Params:       "",
		SizeBytes:    2708805088,
		SizeLabel:    "~2.7 GB",
		DownloadURL:  "https://huggingface.co/mradermacher/GRM-2.5-i1-GGUF/resolve/main/GRM-2.5.i1-Q4_K_M.gguf",
		Filename:     "GRM-2.5.i1-Q4_K_M.gguf",
		Description:  "Model with excellent tool calling support",
		ToolCallTier: "excellent",
		IsDefault:    false,
	},
	{
		ID:           "qwen3-4b",
		DisplayName:  "Qwen3 4B",
		Params:       "4B",
		SizeBytes:    2684354560,
		SizeLabel:    "~2.5 GB",
		DownloadURL:  "https://huggingface.co/bartowski/Qwen_Qwen3-4B-GGUF/resolve/main/Qwen3-4B-Q4_K_M.gguf",
		Filename:     "Qwen3-4B-Q4_K_M.gguf",
		Description:  "Compact 4B parameter model with excellent tool calling",
		ToolCallTier: "excellent",
	},
	{
		ID:           "phi4-mini",
		DisplayName:  "Phi-4 Mini Instruct",
		Params:       "3.8B",
		SizeBytes:    2684354560,
		SizeLabel:    "~2.5 GB",
		DownloadURL:  "https://huggingface.co/bartowski/microsoft_Phi-4-mini-instruct-GGUF/resolve/main/microsoft_Phi-4-mini-instruct-Q4_K_M.gguf",
		Filename:     "microsoft_Phi-4-mini-instruct-Q4_K_M.gguf",
		Description:  "Microsoft Phi-4 Mini with excellent tool calling",
		ToolCallTier: "excellent",
	},
	{
		ID:           "qwen3-8b",
		DisplayName:  "Qwen3 8B",
		Params:       "8B",
		SizeBytes:    5368709120,
		SizeLabel:    "~5.0 GB",
		DownloadURL:  "https://huggingface.co/bartowski/Qwen_Qwen3-8B-GGUF/resolve/main/Qwen3-8B-Q4_K_M.gguf",
		Filename:     "Qwen3-8B-Q4_K_M.gguf",
		Description:  "Larger 8B Qwen3 model with excellent tool calling",
		ToolCallTier: "excellent",
	},
	{
		ID:           "gemma3-4b",
		DisplayName:  "Gemma 3 4B IT",
		Params:       "4B",
		SizeBytes:    2684354560,
		SizeLabel:    "~2.5 GB",
		DownloadURL:  "https://huggingface.co/bartowski/google_gemma-3-4b-it-GGUF/resolve/main/google_gemma-3-4b-it-Q4_K_M.gguf",
		Filename:     "google_gemma-3-4b-it-Q4_K_M.gguf",
		Description:  "Google Gemma 3 instruction-tuned with good tool calling",
		ToolCallTier: "good",
	},
	{
		ID:           "qwen25-7b",
		DisplayName:  "Qwen 2.5 7B Instruct",
		Params:       "7B",
		SizeBytes:    5033164800,
		SizeLabel:    "~4.7 GB",
		DownloadURL:  "https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf",
		Filename:     "Qwen2.5-7B-Instruct-Q4_K_M.gguf",
		Description:  "Qwen 2.5 7B instruction-tuned with good tool calling",
		ToolCallTier: "good",
	},
	{
		ID:           "smollm3-3b",
		DisplayName:  "SmolLM3 3B",
		Params:       "3B",
		SizeBytes:    2063597568,
		SizeLabel:    "~1.9 GB",
		DownloadURL:  "https://huggingface.co/bartowski/HuggingFaceTB_SmolLM3-3B-GGUF/resolve/main/SmolLM3-3B-Q4_K_M.gguf",
		Filename:     "SmolLM3-3B-Q4_K_M.gguf",
		Description:  "HuggingFace SmolLM3 compact model with good tool calling",
		ToolCallTier: "good",
	},
	{
		ID:           "glm4-9b",
		DisplayName:  "GLM-4 9B",
		Params:       "9B",
		SizeBytes:    6635241472,
		SizeLabel:    "~6.2 GB",
		DownloadURL:  "https://huggingface.co/bartowski/THUDM_GLM-4-9B-0414-GGUF/resolve/main/GLM-4-9B-0414-Q4_K_M.gguf",
		Filename:     "GLM-4-9B-0414-Q4_K_M.gguf",
		Description:  "THUDM GLM-4 9B with good tool calling",
		ToolCallTier: "good",
	},
	{
		ID:           "qwen25-3b",
		DisplayName:  "Qwen 2.5 3B Instruct",
		Params:       "3B",
		SizeBytes:    2075918336,
		SizeLabel:    "~1.9 GB",
		DownloadURL:  "https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf",
		Filename:     "Qwen2.5-3B-Instruct-Q4_K_M.gguf",
		Description:  "Qwen 2.5 3B instruction-tuned with good tool calling",
		ToolCallTier: "good",
	},
	{
		ID:           "llama32-3b",
		DisplayName:  "Llama 3.2 3B Instruct",
		Params:       "3B",
		SizeBytes:    2172649472,
		SizeLabel:    "~2.0 GB",
		DownloadURL:  "https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		Filename:     "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		Description:  "Meta Llama 3.2 3B with limited tool calling",
		ToolCallTier: "limited",
	},
}

// GetCatalog returns the full model catalog.
func GetCatalog() []ModelEntry {
	return modelCatalog
}

// FindModelByID looks up a model by its ID. Returns nil if not found.
func FindModelByID(id string) *ModelEntry {
	for i := range modelCatalog {
		if modelCatalog[i].ID == id {
			return &modelCatalog[i]
		}
	}
	return nil
}

// GetDefaultModel returns the default model entry.
func GetDefaultModel() *ModelEntry {
	for i := range modelCatalog {
		if modelCatalog[i].IsDefault {
			return &modelCatalog[i]
		}
	}
	return &modelCatalog[0]
}
