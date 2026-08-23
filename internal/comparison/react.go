package comparison

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
)

const (
	reactSystemPrompt = `You are a documentation generator. You have access to filesystem tools to explore a Go codebase. Read the relevant source files, understand the code, and produce the requested documentation. Call tools as needed. When you have gathered enough information, output the final documentation as markdown.`

	reactDatanalSystemPrompt = `You are a data analyst. You have access to filesystem tools (read_file, list_dir, search_files) to read and analyze structured data files. Read the specified data file, parse it as CSV/tabular data, and answer the question precisely. When analyzing CSV data: pay attention to column headers in the first row, handle empty/blank values explicitly, and count/group/filter as requested. Show your work — state the total record count and intermediate calculations before giving your final answer.`

	reactResearchSystemPrompt = `You are a web researcher with access to web_search and web_browse tools. Your job is to find, verify, and synthesize information from the internet.

IMPORTANT instructions:
- Use web_search to find relevant sources for the research topic
- Use web_browse to read full page content from the most promising URLs returned by search
- Cross-reference information across multiple sources for accuracy
- Always cite your sources with the actual URLs you visited
- Do NOT fabricate or hallucinate URLs — only cite URLs you actually browsed
- Synthesize findings into a coherent, well-structured analysis
- When you have gathered enough information, output the final research synthesis as markdown`

	localReActSystemPrompt = `You are a documentation generator running on a local model. You have access to filesystem tools to explore a Go codebase. Read the relevant source files, understand the code, and produce the requested documentation.

IMPORTANT instructions for tool usage:
- Always start by listing the project directory to understand the structure
- Read specific files that are relevant to the documentation task
- Use search_files to find specific patterns or function definitions
- When you have gathered enough information, output the final documentation as markdown
- Be thorough — explore multiple files and directories before writing
- Do NOT hallucinate file contents — always read files before documenting them`
)

type piReActOptions struct {
	Task         ComparisonTask
	Condition    string // ConditionCloudReAct or ConditionLocalReAct
	Provider     string
	Model        string
	APIKey       string
	BaseURL      string
	SystemPrompt string
	Pricing      PricingTable
	IsLocal      bool
	OutputDir    string
}

// RunReAct executes a ReAct loop for a single task using pi-coder against the cloud model.
func RunReAct(ctx context.Context, task ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	return RunReActWithEndpoint(ctx, task, pricing, "")
}

// RunReActWithEndpoint is like RunReAct but allows overriding the API endpoint (for testing or proxying).
func RunReActWithEndpoint(ctx context.Context, task ComparisonTask, pricing PricingTable, endpoint string) (ComparisonResult, error) {
	sysPrompt := reactSystemPrompt
	if task.Category == CategoryDatanal {
		sysPrompt = reactDatanalSystemPrompt
	} else if task.Category == CategoryResearch {
		sysPrompt = reactResearchSystemPrompt
	}

	cfg := config.Get()
	provider := cfg.CloudProvider
	if provider == "" {
		provider = "google"
	}
	model := config.GetCloudModel()
	if model == "" {
		model = "gemini-flash-latest"
	}
	apiKey := config.GetCloudAPIKey()

	var baseURL string
	if endpoint != "" {
		baseURL = strings.TrimSuffix(endpoint, "/chat/completions")
		provider = "custom-cloud"
		if apiKey == "" {
			apiKey = "none"
		}
	}

	opts := piReActOptions{
		Task:         task,
		Condition:    ConditionCloudReAct,
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		SystemPrompt: sysPrompt,
		Pricing:      pricing,
		IsLocal:      false,
	}

	return runPiReAct(ctx, opts)
}

// RunLocalReAct executes a ReAct loop against the configured local worker backend using pi-coder.
func RunLocalReAct(ctx context.Context, task ComparisonTask, pricing PricingTable, outputDir string) (ComparisonResult, error) {
	cfg := config.Get()
	provider := "tzro-local"
	model := "local"
	apiKey := "none"

	if cfg.InferenceBackend.Type == "openai-compatible" && cfg.InferenceBackend.URL != "" {
		provider = "tzro-configured-backend"
		if cfg.InferenceBackend.Model != "" {
			model = cfg.InferenceBackend.Model
		}
		if cfg.InferenceBackend.APIKey != "" {
			resolvedKey := cfg.InferenceBackend.APIKey
			if strings.HasPrefix(resolvedKey, "$") {
				resolvedKey = os.Getenv(strings.TrimPrefix(resolvedKey, "$"))
			}
			if resolvedKey != "" {
				apiKey = resolvedKey
			}
		}
	}

	localEndpoint, err := resolveLocalEndpoint()
	if err != nil {
		return ComparisonResult{
			TaskID:    task.ID,
			TaskTier:  task.Tier,
			Condition: ConditionLocalReAct,
			Error:     fmt.Sprintf("failed to resolve local sidecar: %v", err),
		}, err
	}

	baseURL := strings.TrimSuffix(localEndpoint, "/chat/completions")

	opts := piReActOptions{
		Task:         task,
		Condition:    ConditionLocalReAct,
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		SystemPrompt: localReActSystemPrompt,
		Pricing:      pricing,
		IsLocal:      true,
		OutputDir:    outputDir,
	}

	return runPiReAct(ctx, opts)
}

// runPiReAct executes the pi-coder CLI in single-shot JSON mode, captures events,
// counts tool calls, tracks token consumption, and extracts the final text response.
func runPiReAct(ctx context.Context, opts piReActOptions) (ComparisonResult, error) {
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	piPath, err := exec.LookPath("pi")
	if err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     "pi CLI not found in PATH. Please install with: npm install -g @earendil-works/pi-coding-agent",
		}, fmt.Errorf("pi executable not found in PATH: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "tzro-pi-config-*")
	if err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     fmt.Sprintf("failed to create temp config directory: %v", err),
		}, err
	}
	defer os.RemoveAll(tempDir)

	if opts.BaseURL != "" {
		modelsJSON := fmt.Sprintf(`{
  "providers": {
    %q: {
      "baseUrl": %q,
      "api": "openai-completions",
      "apiKey": %q,
      "compat": {
        "supportsDeveloperRole": false,
        "supportsReasoningEffort": false
      },
      "models": [
        { "id": %q }
      ]
    }
  }
}
`, opts.Provider, opts.BaseURL, opts.APIKey, opts.Model)

		if err := os.WriteFile(filepath.Join(tempDir, "models.json"), []byte(modelsJSON), 0600); err != nil {
			return ComparisonResult{
				TaskID:    opts.Task.ID,
				TaskTier:  opts.Task.Tier,
				Condition: opts.Condition,
				Error:     fmt.Sprintf("failed to write models.json: %v", err),
			}, err
		}
	}

	// Write custom tools extension to make tzro's tool suite (read_file, list_dir, search_files, web_search, web_browse, write_file) available
	extPath := filepath.Join(tempDir, "tzro-tools.js")
	extContent := generateToolsExtension()
	if err := os.WriteFile(extPath, []byte(extContent), 0600); err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     fmt.Sprintf("failed to write tools extension: %v", err),
		}, err
	}

	args := []string{"--mode", "json", "--no-session", "--no-context-files", "-e", extPath, "-p"}
	if opts.Provider != "" {
		args = append(args, "--provider", opts.Provider)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.APIKey != "" {
		args = append(args, "--api-key", opts.APIKey)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	args = append(args, opts.Task.Prompt)

	cmd := exec.CommandContext(ctx, piPath, args...)
	cmd.Stdin = bytes.NewReader(nil) // prevent pi from waiting for piped stdin

	env := os.Environ()
	if opts.BaseURL != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+tempDir)
	}
	if opts.APIKey != "" {
		switch opts.Provider {
		case "google":
			env = append(env, "GEMINI_API_KEY="+opts.APIKey)
		case "openai":
			env = append(env, "OPENAI_API_KEY="+opts.APIKey)
		}
	}
	cmd.Env = env

	cwd, _ := os.Getwd()
	cmd.Dir = cwd

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     fmt.Sprintf("failed to create stdout pipe: %v", err),
		}, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     fmt.Sprintf("failed to create stderr pipe: %v", err),
		}, err
	}

	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return ComparisonResult{
			TaskID:    opts.Task.ID,
			TaskTier:  opts.Task.Tier,
			Condition: opts.Condition,
			Error:     fmt.Sprintf("failed to start pi process: %v", err),
		}, err
	}

	var stderrBuf bytes.Buffer
	go func() {
		_, _ = io.Copy(&stderrBuf, stderr)
	}()

	var finalOutput string
	var toolCallCount int
	var totalPromptTokens int
	var totalCompletionTokens int

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var event struct {
			Type    string `json:"type"`
			Message *struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Usage   *struct {
					Input       int `json:"input"`
					Output      int `json:"output"`
					TotalTokens int `json:"totalTokens"`
				} `json:"usage"`
				ToolResults []json.RawMessage `json:"toolResults"`
			} `json:"message"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Usage   *struct {
					Input       int `json:"input"`
					Output      int `json:"output"`
					TotalTokens int `json:"totalTokens"`
				} `json:"usage"`
			} `json:"messages"`
			AssistantMessageEvent *struct {
				Type    string          `json:"type"`
				Content string          `json:"content"`
				Delta   string          `json:"delta"`
				Partial json.RawMessage `json:"partial"`
			} `json:"assistantMessageEvent"`
		}

		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		if event.AssistantMessageEvent != nil {
			if event.AssistantMessageEvent.Type == "toolcall_start" {
				toolCallCount++
				toolName := ""
				if len(event.AssistantMessageEvent.Partial) > 0 {
					var partial struct {
						Content []struct {
							Type string `json:"type"`
							Name string `json:"name"`
						} `json:"content"`
					}
					if err := json.Unmarshal(event.AssistantMessageEvent.Partial, &partial); err == nil {
						for _, item := range partial.Content {
							if item.Name != "" {
								toolName = item.Name
								break
							}
						}
					}
				}
				if toolName != "" {
					fmt.Fprintf(os.Stderr, "  [%s] Tool call #%d: %s\n", opts.Condition, toolCallCount, toolName)
				} else {
					fmt.Fprintf(os.Stderr, "  [%s] Tool call #%d\n", opts.Condition, toolCallCount)
				}
			}
		}

		if event.Type == "agent_end" && len(event.Messages) > 0 {
			var agentToolCalls int
			var promptToks, complToks int
			for _, msg := range event.Messages {
				if msg.Role == "assistant" {
					if msg.Usage != nil {
						promptToks += msg.Usage.Input
						complToks += msg.Usage.Output
					}
					texts, tcCount := parsePiContent(msg.Content)
					if tcCount > 0 {
						agentToolCalls += tcCount
					}
					if len(texts) > 0 {
						finalOutput = strings.Join(texts, "\n")
					}
				}
			}
			if agentToolCalls > 0 && toolCallCount == 0 {
				toolCallCount = agentToolCalls
			}
			if promptToks > 0 || complToks > 0 {
				totalPromptTokens = promptToks
				totalCompletionTokens = complToks
			}
		} else if (event.Type == "turn_end" || event.Type == "message_end") && event.Message != nil {
			if event.Message.Role == "assistant" {
				texts, tcCount := parsePiContent(event.Message.Content)
				if len(texts) > 0 {
					finalOutput = strings.Join(texts, "\n")
				}
				if tcCount > 0 && toolCallCount == 0 {
					toolCallCount += tcCount
				}
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		fmt.Fprintf(os.Stderr, "[%s] Scanner error: %v\n", opts.Condition, scanErr)
	}

	cmdErr := cmd.Wait()
	wallClockMs := time.Since(startTime).Milliseconds()

	if cmdErr != nil && finalOutput == "" {
		errDetails := stderrBuf.String()
		if errDetails == "" {
			errDetails = cmdErr.Error()
		}
		return ComparisonResult{
			TaskID:      opts.Task.ID,
			TaskTier:    opts.Task.Tier,
			Condition:   opts.Condition,
			WallClockMs: wallClockMs,
			Error:       fmt.Sprintf("pi process failed: %s", strings.TrimSpace(errDetails)),
		}, cmdErr
	}

	duration := time.Since(startTime).Seconds()
	speed := 0.0
	if duration > 0 && totalCompletionTokens > 0 {
		speed = float64(totalCompletionTokens) / duration
	}
	tracker.Record(!opts.IsLocal, totalPromptTokens, totalCompletionTokens, duration, speed)

	localUsage, cloudUsage := tracker.GetUsage()
	var estCost float64
	if !opts.IsLocal {
		estCost = EstimateCost(cloudUsage, inference.TokenUsage{}, opts.Pricing)
	}

	return ComparisonResult{
		TaskID:        opts.Task.ID,
		TaskTier:      opts.Task.Tier,
		Condition:     opts.Condition,
		CloudTokens:   cloudUsage,
		LocalTokens:   localUsage,
		WallClockMs:   wallClockMs,
		EstCostUSD:    estCost,
		ToolCallCount: toolCallCount,
		OutputText:    finalOutput,
	}, nil
}

// parsePiContent extracts text content strings and tool call occurrences from message content blocks.
func parsePiContent(raw json.RawMessage) ([]string, int) {
	if len(raw) == 0 {
		return nil, 0
	}

	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if str != "" {
			return []string{str}, 0
		}
		return nil, 0
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var texts []string
		var toolCalls int
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			} else if b.Type == "toolCall" || b.Type == "tool_call" {
				toolCalls++
			}
		}
		return texts, toolCalls
	}

	return nil, 0
}

// generateToolsExtension creates a JavaScript extension for pi-coder that registers
// tzro's core tool suite (read_file, list_dir, search_files, write_file, web_search, web_browse).
func generateToolsExtension() string {
	return `import fs from "node:fs";
import path from "node:path";
import { execSync } from "node:child_process";

export default function(pi) {
  // read_file
  pi.registerTool({
    name: "read_file",
    description: "Read file content with optional line range. Returns raw content (max 200 lines per call).",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string", description: "The path of the file to read." },
        start_line: { type: "integer", description: "The starting line number to read (1-indexed, optional)." },
        end_line: { type: "integer", description: "The ending line number to read (1-indexed, inclusive, optional)." },
        offset: { type: "integer", description: "Line offset (alias for start_line)." },
        limit: { type: "integer", description: "Number of lines to read (alias for count)." }
      },
      required: ["path"]
    },
    async execute(toolCallId, params) {
      try {
        const targetPath = params.path || params.file_path || params.filePath;
        if (!targetPath) return { content: [{ type: "text", text: "Error: missing path parameter" }], isError: true };
        const filePath = path.resolve(process.cwd(), targetPath);
        const content = fs.readFileSync(filePath, "utf8");
        const lines = content.split("\n");
        let start = (params.start_line || params.offset || 1) - 1;
        if (start < 0) start = 0;
        let end = lines.length;
        if (params.end_line) {
          end = params.end_line;
        } else if (params.limit) {
          end = start + params.limit;
        }
        if (end > lines.length) end = lines.length;
        const selected = lines.slice(start, end).join("\n");
        return {
          content: [{ type: "text", text: selected }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: "Error reading file: " + err.message }],
          isError: true
        };
      }
    }
  });

  // list_dir
  pi.registerTool({
    name: "list_dir",
    description: "List the contents of a directory with file sizes and types.",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string", description: "The path of the directory to list." },
        recursive: { type: "boolean", description: "Whether to list subdirectories recursively (optional)." }
      },
      required: ["path"]
    },
    async execute(toolCallId, params) {
      try {
        const targetPath = params.path || params.dir_path || ".";
        const dirPath = path.resolve(process.cwd(), targetPath);
        const entries = fs.readdirSync(dirPath, { withFileTypes: true });
        const list = entries.map(e => {
          let size = 0;
          try {
            if (e.isFile()) size = fs.statSync(path.join(dirPath, e.name)).size;
          } catch (_) {}
          return {
            name: e.name,
            is_dir: e.isDirectory(),
            size: size
          };
        });
        return {
          content: [{ type: "text", text: JSON.stringify(list, null, 2) }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: "Error listing directory: " + err.message }],
          isError: true
        };
      }
    }
  });

  // search_files
  pi.registerTool({
    name: "search_files",
    description: "Search for a text pattern across files using ripgrep. Returns matching file paths and line content.",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string", description: "The root directory to search in." },
        pattern: { type: "string", description: "The pattern or query string to search for." },
        query: { type: "string", description: "Query string (alias for pattern)." },
        regex: { type: "boolean", description: "Whether pattern is a regex." }
      },
      required: ["path", "pattern"]
    },
    async execute(toolCallId, params) {
      try {
        const targetPath = params.path || ".";
        const query = params.pattern || params.query || "";
        const searchPath = path.resolve(process.cwd(), targetPath);
        const rgArgs = ["-n", "--no-heading"];
        if (!params.regex) rgArgs.push("-F");
        rgArgs.push(query, searchPath);
        const out = execSync("rg " + rgArgs.map(a => JSON.stringify(a)).join(" "), { encoding: "utf8", maxBuffer: 10*1024*1024 });
        return {
          content: [{ type: "text", text: out }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: err.stdout || "No matches found" }],
          details: {}
        };
      }
    }
  });

  // write_file
  pi.registerTool({
    name: "write_file",
    description: "Write content to a file. Use this to save your final documentation output.",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string", description: "The file path to write to" },
        content: { type: "string", description: "The content to write to the file" }
      },
      required: ["path", "content"]
    },
    async execute(toolCallId, params) {
      try {
        const filePath = path.resolve(process.cwd(), params.path);
        fs.mkdirSync(path.dirname(filePath), { recursive: true });
        fs.writeFileSync(filePath, params.content, "utf8");
        return {
          content: [{ type: "text", text: JSON.stringify({ status: "ok", bytes_written: Buffer.byteLength(params.content, "utf8") }) }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: "Error writing file: " + err.message }],
          isError: true
        };
      }
    }
  });

  // web_search
  pi.registerTool({
    name: "web_search",
    description: "Search the web for current information. Returns a list of results with titles, URLs, and snippets.",
    parameters: {
      type: "object",
      properties: {
        query: { type: "string", description: "Search query" }
      },
      required: ["query"]
    },
    async execute(toolCallId, params) {
      try {
        const query = encodeURIComponent(params.query);
        const res = await fetch("https://html.duckduckgo.com/html/?q=" + query, {
          headers: { "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)" }
        });
        const html = await res.text();
        const results = [];
        const regex = /<a[^>]*class="result__snippet"[^>]*href="([^"]*)"[^>]*>(.*?)<\/a>/g;
        let match;
        while ((match = regex.exec(html)) !== null && results.length < 5) {
          results.push({ url: match[1], snippet: match[2].replace(/<[^>]*>/g, "") });
        }
        return {
          content: [{ type: "text", text: JSON.stringify(results, null, 2) }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: "Search error: " + err.message }],
          isError: true
        };
      }
    }
  });

  // web_browse
  pi.registerTool({
    name: "web_browse",
    description: "Fetch a web page URL and return its text content.",
    parameters: {
      type: "object",
      properties: {
        url: { type: "string", description: "The URL to browse" }
      },
      required: ["url"]
    },
    async execute(toolCallId, params) {
      try {
        const res = await fetch(params.url, {
          headers: { "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)" }
        });
        const text = await res.text();
        const cleaned = text.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, "")
                            .replace(/<style\b[^<]*(?:(?!<\/style>)<[^<]*)*<\/style>/gi, "")
                            .replace(/<[^>]+>/g, " ")
                            .replace(/\s+/g, " ")
                            .trim()
                            .slice(0, 10000);
        return {
          content: [{ type: "text", text: cleaned }],
          details: {}
        };
      } catch (err) {
        return {
          content: [{ type: "text", text: "Browse error: " + err.message }],
          isError: true
        };
      }
    }
  });
}
`
}

// ────────────────────────────────────────────────────────────────────────────
// Types retained for mock compatibility in test suites
// ────────────────────────────────────────────────────────────────────────────

const (
	maxReActIterations   = 50
	maxAccumulatedTokens = 200000
)

type reactMessage struct {
	Role       string          `json:"role"`
	Content    interface{}     `json:"content,omitempty"`
	ToolCalls  []reactToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type reactToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}

type reactToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  interface{} `json:"parameters"`
	} `json:"function"`
}

type reactCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []reactMessage `json:"messages"`
	Tools       []reactToolDef `json:"tools"`
	Temperature float64        `json:"temperature"`
}

type reactCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content          *string         `json:"content"`
			ReasoningContent *string         `json:"reasoning_content,omitempty"`
			ToolCalls        []reactToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// resolveLocalEndpoint discovers the configured local inference endpoint URL
// (either from an active InferenceBackend like openai-compatible, or the local llama-server sidecar).
func resolveLocalEndpoint() (string, error) {
	cfg := config.Get()
	if cfg.InferenceBackend.Type == "openai-compatible" && cfg.InferenceBackend.URL != "" {
		url := cfg.InferenceBackend.URL
		if !strings.HasSuffix(url, "/chat/completions") {
			trimmed := strings.TrimSuffix(url, "/")
			if strings.Contains(trimmed, "/v1") || strings.Contains(trimmed, "/v2") {
				url = trimmed + "/chat/completions"
			} else {
				url = trimmed + "/v1/chat/completions"
			}
		}
		return url, nil
	}

	ctx := context.Background()

	status, activePort, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.GlobalLocalModel.Start(ctx); err != nil {
			return "", fmt.Errorf("sidecar auto-start failed: %w", err)
		}
		// Wait for healthy
		_, activePort, _, _, _ = inference.GlobalLocalModel.GetStatusInfo()
		fmt.Fprintf(os.Stderr, "[LocalReAct] Waiting for sidecar health on port %d...\n", activePort)
		for attempt := range 30 {
			healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
			resp, err := http.Get(healthURL)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				fmt.Fprintf(os.Stderr, "[LocalReAct] Sidecar healthy after %d attempts\n", attempt+1)
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(1 * time.Second)
		}
	}

	if activePort <= 0 {
		return "", fmt.Errorf("sidecar port not available (status=%s, port=%d)", status, activePort)
	}

	return fmt.Sprintf("http://localhost:%d/v1/chat/completions", activePort), nil
}

