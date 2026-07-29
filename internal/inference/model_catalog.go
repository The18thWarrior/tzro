package inference

// ModelEntry represents a downloadable GGUF model in the catalog.
type ModelEntry struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	Params          string `json:"params"`
	SizeBytes       int64  `json:"sizeBytes"`
	SizeLabel       string `json:"sizeLabel"`
	DownloadURL     string `json:"downloadUrl"`
	Filename        string `json:"filename"`
	Description     string `json:"description"`
	ToolCallTier    string `json:"toolCallTier"`
	IsDefault       bool   `json:"isDefault"`
	Role            string `json:"role,omitempty"`            // "router" | "worker" | "" (empty = worker)
	IsDefaultRouter bool   `json:"isDefaultRouter,omitempty"` // true = default router model for dual-sidecar

	// CompanionMMProj is the optional multimodal projector for vision-capable models.
	// When non-nil, this projector is auto-downloaded alongside the base model to enable
	// local vision features (PDF OCR, image analysis) without external dependencies.
	CompanionMMProj *CompanionFile `json:"companionMmproj,omitempty"`

	// CompanionMTP is the optional Multi-Token Prediction draft model.
	// When non-nil, this model is auto-downloaded alongside the base model and used
	// for MTP speculative decoding (--spec-type draft-mtp) for faster inference.
	CompanionMTP *CompanionFile `json:"companionMtp,omitempty"`
}

// CompanionFile describes an auxiliary GGUF file that accompanies a base model.
type CompanionFile struct {
	DownloadURL string `json:"downloadUrl"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"sizeBytes"`
	SizeLabel   string `json:"sizeLabel"`
}

var modelCatalog = []ModelEntry{
	{
		ID:           "gemma-4-e4b-it-qat",
		DisplayName:  "Gemma 4 E4B IT QAT",
		Params:       "E4B",
		SizeBytes:    4215693760,
		SizeLabel:    "~3.9 GB",
		DownloadURL:  "https://huggingface.co/unsloth/gemma-4-E4B-it-qat-GGUF/resolve/main/gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf?download=true",
		Filename:     "gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf",
		Description:  "Default Gemma 4 E4B model with QAT calibration",
		ToolCallTier: "excellent",
		IsDefault:    false,
		CompanionMMProj: &CompanionFile{
			DownloadURL: "https://huggingface.co/ggml-org/gemma-4-E4B-it-GGUF/resolve/main/mmproj-gemma-4-E4B-it-Q8_0.gguf",
			Filename:    "mmproj-gemma-4-E4B-it-Q8_0.gguf",
			SizeBytes:   559874528,
			SizeLabel:   "~534 MB",
		},
	},
	{
		ID:           "gemma4-12b-agentic-fable5",
		DisplayName:  "Gemma 4 12B Agentic Fable5",
		Params:       "12B",
		SizeBytes:    6087086624,
		SizeLabel:    "~5.7 GB",
		DownloadURL:  "https://huggingface.co/yuxinlu1/gemma-4-12B-agentic-fable5-composer2.5-v2-3.5x-tau2-GGUF/resolve/main/gemma4-v2-Q4_K_M.gguf",
		Filename:     "gemma4-v2-Q4_K_M.gguf",
		Description:  "Gemma 4 12B fine-tuned for agentic coding, tool-use, and reasoning",
		ToolCallTier: "excellent",
		IsDefault:    false,
		CompanionMTP: &CompanionFile{
			DownloadURL: "https://huggingface.co/yuxinlu1/gemma-4-12B-agentic-fable5-composer2.5-v2-3.5x-tau2-GGUF/resolve/main/MTP/gemma-4-12B-it-MTP-Q8_0.gguf",
			Filename:    "gemma-4-12B-it-MTP-Q8_0.gguf",
			SizeBytes:   1073741824,
			SizeLabel:   "~1.0 GB",
		},
	},
	{
		ID:           "qwythos-9b",
		DisplayName:  "Qwythos 9B Claude Mythos",
		Params:       "9B",
		SizeBytes:    5887668160,
		SizeLabel:    "~5.5 GB",
		DownloadURL:  "https://huggingface.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF/resolve/main/Qwythos-9B-Claude-Mythos-5-1M-MTP-Q4_K_M.gguf",
		Filename:     "Qwythos-9B-Claude-Mythos-5-1M-MTP-Q4_K_M.gguf",
		Description:  "Qwen 3.5 based 9B model with built-in MTP, 1M context, reasoning, function calling, and vision",
		ToolCallTier: "excellent",
		IsDefault:    false,
		CompanionMMProj: &CompanionFile{
			DownloadURL: "https://huggingface.co/empero-ai/Qwythos-9B-Claude-Mythos-5-1M-GGUF/resolve/main/mmproj-Qwythos-9B-Claude-Mythos-5-1M-F16.gguf",
			Filename:    "mmproj-Qwythos-9B-Claude-Mythos-5-1M-F16.gguf",
			SizeBytes:   918165472,
			SizeLabel:   "~876 MB",
		},
	},
	{
		ID:           "agents-a1-4b",
		DisplayName:  "Agents A1 4B",
		Params:       "4B",
		SizeBytes:    2708805312,
		SizeLabel:    "~2.5 GB",
		DownloadURL:  "https://huggingface.co/InternScience/Agents-A1-4B-Q4_K_M-GGUF/resolve/main/Agents-A1-4B-Q4_K_M.gguf",
		Filename:     "Agents-A1-4B-Q4_K_M.gguf",
		Description:  "Long-horizon agentic model from InternScience, optimized for multi-step search, synthesis, instruction following, and tool calling",
		ToolCallTier: "excellent",
		IsDefault:    true,
	},
	{
		ID:           "qwopus-3.5-4b-mtp",
		DisplayName:  "Qwopus 3.5 4B Coder MTP",
		Params:       "4B",
		SizeBytes:    2985000000,
		SizeLabel:    "~2.8 GB",
		DownloadURL:  "https://huggingface.co/Jackrong/Qwopus3.5-4B-Coder-MTP-GGUF/resolve/main/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf",
		Filename:     "Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf",
		Description:  "Qwen 3.5 4B optimized for agentic coding and multi-turn tool-calling with distilled Claude Opus traces and native MTP speculative decoding",
		ToolCallTier: "excellent",
		IsDefault:    false,
		CompanionMMProj: &CompanionFile{
			DownloadURL: "https://huggingface.co/Jackrong/Qwopus3.5-4B-Coder-MTP-GGUF/resolve/main/mmproj-F32.gguf",
			Filename:    "mmproj-F32.gguf",
			SizeBytes:   559874528,
			SizeLabel:   "~534 MB",
		},
	},
	{
		ID:           "nanbeige42-3b",
		DisplayName:  "Nanbeige 4.2 3B",
		Params:       "3B",
		SizeBytes:    2574807936,
		SizeLabel:    "~2.4 GB",
		DownloadURL:  "https://huggingface.co/owao/Nanbeige4.2-3B-GGUF/resolve/main/nanbeige4.2-3b-Q4_K_M.gguf",
		Filename:     "nanbeige4.2-3b-Q4_K_M.gguf",
		Description:  "Nanbeige 4.2 3B instruction-tuned model with good tool calling",
		ToolCallTier: "good",
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
	// Router models — small, fast models for classification, routing, and probe navigation
	{
		ID:              "minicpm5-1b-opus-fable5",
		DisplayName:     "MiniCPM5 1B Claude Opus Fable5 Thinking",
		Params:          "1B",
		SizeBytes:       1267597312,
		SizeLabel:       "~1.2 GB",
		DownloadURL:     "https://huggingface.co/GnLOLot/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-GGUF/resolve/main/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf?download=true",
		Filename:        "MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf",
		Description:     "MiniCPM5 1B distilled from Claude Opus traces with thinking — optimized for fast routing, classification, and probe navigation",
		ToolCallTier:    "good",
		Role:            "router",
		IsDefaultRouter: true,
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

// FindModelByFilename looks up a model by its GGUF filename. Returns nil if not found.
func FindModelByFilename(filename string) *ModelEntry {
	for i := range modelCatalog {
		if modelCatalog[i].Filename == filename {
			return &modelCatalog[i]
		}
	}
	return nil
}

// GetDefaultModel returns the default worker model entry.
func GetDefaultModel() *ModelEntry {
	for i := range modelCatalog {
		if modelCatalog[i].IsDefault {
			return &modelCatalog[i]
		}
	}
	return &modelCatalog[0]
}

// GetDefaultRouterModel returns the default router model entry.
// Returns nil if no model is marked as the default router.
func GetDefaultRouterModel() *ModelEntry {
	for i := range modelCatalog {
		if modelCatalog[i].IsDefaultRouter {
			return &modelCatalog[i]
		}
	}
	return nil
}

// FindRouterModels returns all catalog entries with Role == "router".
func FindRouterModels() []ModelEntry {
	var models []ModelEntry
	for _, m := range modelCatalog {
		if m.Role == "router" {
			models = append(models, m)
		}
	}
	return models
}
