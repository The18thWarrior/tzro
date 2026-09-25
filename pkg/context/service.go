package context

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tzro/pkg/ast"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
	"tzro/pkg/tokenizer"
)

// ContextRequest defines the parameters for context pack assembly.
type ContextRequest struct {
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	Query         string `json:"query,omitempty"`
	Symbol        string `json:"symbol,omitempty"`
	File          string `json:"file,omitempty"`
	Budget        int    `json:"budget,omitempty"`
	Format        string `json:"format,omitempty"` // "markdown" | "json"
	Output        string `json:"output,omitempty"` // file path
	Force         bool   `json:"force,omitempty"`  // overwrite output file
}

// ContextResult contains the assembled pack and its serialized representation.
type ContextResult struct {
	Pack       *ContextPack
	Formatted  string
	Format     string
	OutputPath string
}

// ContextService coordinates query-based and symbol-anchored context pack assembly.
type ContextService struct {
	store        *store.Store
	policyEngine *dlp.PolicyEngine
	registry     *AdapterRegistry
}

// NewContextService creates a new ContextService.
func NewContextService(s *store.Store, policyEngine *dlp.PolicyEngine) *ContextService {
	return &ContextService{
		store:        s,
		policyEngine: policyEngine,
		registry:     NewAdapterRegistry(s, policyEngine),
	}
}

// Validate validates the request fields.
func (req *ContextRequest) Validate() error {
	// Must have canonical workspace root
	if req.WorkspaceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
		req.WorkspaceRoot = cwd
	}

	canonicalRoot, err := ResolveWorkspaceRoot(req.WorkspaceRoot)
	if err == nil {
		req.WorkspaceRoot = canonicalRoot
	}

	cfg, err := LoadConfig(req.WorkspaceRoot)
	if err != nil {
		return err
	}

	trimmedQuery := strings.TrimSpace(req.Query)
	trimmedSymbol := strings.TrimSpace(req.Symbol)

	if trimmedQuery != "" && trimmedSymbol != "" {
		return errors.New("cannot specify both positional query and --symbol")
	}
	if trimmedQuery == "" && trimmedSymbol == "" {
		return errors.New("must specify either a task query or --symbol")
	}
	if req.File != "" && trimmedSymbol == "" {
		return errors.New("--file requires --symbol")
	}

	if req.Budget == 0 {
		req.Budget = cfg.DefaultBudget
	} else if req.Budget < 0 {
		return fmt.Errorf("%w: budget must be greater than zero", tokenizer.ErrBudgetTooSmall)
	}

	if req.Format == "" {
		req.Format = "markdown"
	}
	normFormat := strings.ToLower(strings.TrimSpace(req.Format))
	if normFormat != "markdown" && normFormat != "json" {
		return fmt.Errorf("unsupported format %q: must be markdown or json", req.Format)
	}
	req.Format = normFormat

	return nil
}

// Execute processes the context request, enforcing token budgets and writing output.
func (s *ContextService) Execute(ctx stdctx.Context, req ContextRequest) (*ContextResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	var pack *ContextPack
	var err error

	if req.Symbol != "" {
		pack, err = s.assembleSymbolContext(ctx, req)
	} else {
		assembler := NewAssembler(s.store, s.policyEngine)
		pack, err = assembler.Assemble(req.WorkspaceRoot, req.Query, req.Budget)
	}

	if err != nil {
		return nil, err
	}

	// Format representation
	var formatted string
	if req.Format == "json" {
		data, mErr := json.MarshalIndent(pack, "", "  ")
		if mErr != nil {
			return nil, fmt.Errorf("failed to format JSON context pack: %w", mErr)
		}
		formatted = string(data) + "\n"
	} else {
		formatted = pack.FormatMarkdown()
	}

	res := &ContextResult{
		Pack:      pack,
		Formatted: formatted,
		Format:    req.Format,
	}

	// Output file writing
	if req.Output != "" {
		outputPath := req.Output
		if !filepath.IsAbs(outputPath) {
			outputPath = filepath.Join(req.WorkspaceRoot, outputPath)
		}

		if _, statErr := os.Stat(outputPath); statErr == nil && !req.Force {
			return nil, fmt.Errorf("output file %q already exists; use --force to overwrite", req.Output)
		}

		outDir := filepath.Dir(outputPath)
		if mkErr := os.MkdirAll(outDir, 0755); mkErr != nil {
			return nil, fmt.Errorf("failed to create output directory: %w", mkErr)
		}

		tmpFile, tmpErr := os.CreateTemp(outDir, ".tzro-context-*.tmp")
		if tmpErr != nil {
			return nil, fmt.Errorf("failed to create temporary file for atomic write: %w", tmpErr)
		}
		tmpPath := tmpFile.Name()

		if _, writeErr := tmpFile.WriteString(formatted); writeErr != nil {
			tmpFile.Close()
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to write context pack to file: %w", writeErr)
		}
		if closeErr := tmpFile.Close(); closeErr != nil {
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to close temporary file: %w", closeErr)
		}

		if renErr := os.Rename(tmpPath, outputPath); renErr != nil {
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to atomically write output file: %w", renErr)
		}

		res.OutputPath = outputPath
	}

	return res, nil
}

// assembleSymbolContext builds a symbol-anchored context pack.
func (s *ContextService) assembleSymbolContext(ctx stdctx.Context, req ContextRequest) (*ContextPack, error) {
	symbolName := strings.TrimSpace(req.Symbol)

	decls, err := s.FindSymbolDeclarations(req.WorkspaceRoot, symbolName, req.File)
	if err != nil {
		return nil, err
	}

	if len(decls) == 0 {
		if req.File != "" {
			return nil, fmt.Errorf("symbol %q not found in file %q", symbolName, req.File)
		}
		return nil, fmt.Errorf("symbol %q not found in workspace", symbolName)
	}

	if len(decls) > 1 {
		var locs []string
		for _, d := range decls {
			locs = append(locs, fmt.Sprintf("  - %s:%d (%s)", d.FilePath, d.StartLine, d.Kind))
		}
		return nil, fmt.Errorf("ambiguous symbol %q found in %d locations:\n%s\nUse --file to disambiguate", symbolName, len(decls), strings.Join(locs, "\n"))
	}

	anchor := decls[0]
	absAnchorPath := filepath.Join(req.WorkspaceRoot, anchor.FilePath)
	sourceBytes, err := os.ReadFile(absAnchorPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read anchor file %s: %w", anchor.FilePath, err)
	}

	// Extract AST declaration span
	span, spanErr := ast.ExtractDeclarationSpan(anchor.FilePath, sourceBytes, anchor.StartLine, anchor.Name, s.store)
	var anchorCode string
	var anchorSig string
	var anchorTokens int
	var anchorHash string
	var anchorKind string

	if spanErr == nil && span != nil {
		anchorCode = span.Code
		anchorSig = span.Signature
		anchorTokens = span.TokenWeight
		anchorHash = span.BodyHash
		anchorKind = span.Kind
	} else {
		anchorCode = string(sourceBytes)
		anchorSig = anchor.Name
		anchorTokens = EstimateTokens(anchorCode)
		anchorKind = anchor.Kind
	}

	anchorItem := PackItem{
		FilePath:     anchor.FilePath,
		SymbolName:   anchor.Name,
		Kind:         anchorKind,
		Signature:    anchorSig,
		StartLine:    anchor.StartLine,
		EndLine:      anchor.EndLine,
		Reason:       fmt.Sprintf("Targeted symbol anchor %q", anchor.Name),
		Score:        1000.0,
		Content:      anchorCode,
		TokenWeight:  anchorTokens,
		Hash:         anchorHash,
		Precision:    "precise",
		Relationship: "anchor",
		Direction:    "",
	}

	// Check if anchor fits budget, degrading to signature stub if needed
	stubContent := anchorItem.Signature
	if stubContent == "" {
		stubContent = fmt.Sprintf("// %s declaration", anchor.Name)
	} else if !strings.Contains(stubContent, "{") {
		stubContent = stubContent + " { /* body omitted to fit budget */ }"
	}
	stubTokens := EstimateTokens(stubContent)

	if anchorItem.TokenWeight > req.Budget {
		if stubTokens <= req.Budget {
			anchorItem.Content = stubContent
			anchorItem.TokenWeight = stubTokens
		} else {
			return nil, fmt.Errorf("%w: budget %d cannot fit minimum representation of symbol anchor %q (%d tokens required)", tokenizer.ErrBudgetTooSmall, req.Budget, anchor.Name, stubTokens)
		}
	}

	// Outgoing callee discovery
	var candidateItems []PackItem
	seenItems := make(map[string]bool)
	seenItems[fmt.Sprintf("%s:%d", anchor.FilePath, anchor.StartLine)] = true

	calleeNames := ExtractOutgoingCalls(anchor.FilePath, sourceBytes, anchor.StartLine, anchor.EndLine)
	for _, calleeName := range calleeNames {
		if calleeName == anchor.Name {
			continue
		}
		cDecls, cErr := s.FindSymbolDeclarations(req.WorkspaceRoot, calleeName, "")
		if cErr == nil && len(cDecls) == 1 {
			cDecl := cDecls[0]
			key := fmt.Sprintf("%s:%d", cDecl.FilePath, cDecl.StartLine)
			if seenItems[key] {
				continue
			}
			seenItems[key] = true

			cSrc, rErr := os.ReadFile(filepath.Join(req.WorkspaceRoot, cDecl.FilePath))
			if rErr == nil {
				cSpan, _ := ast.ExtractDeclarationSpan(cDecl.FilePath, cSrc, cDecl.StartLine, cDecl.Name, s.store)
				var cCode string
				var cSig string
				var cWeight int
				var cHash string
				if cSpan != nil {
					cCode = cSpan.Code
					cSig = cSpan.Signature
					cWeight = cSpan.TokenWeight
					cHash = cSpan.BodyHash
				} else {
					cCode = string(cSrc)
					cWeight = EstimateTokens(cCode)
				}
				candidateItems = append(candidateItems, PackItem{
					FilePath:     cDecl.FilePath,
					SymbolName:   cDecl.Name,
					Kind:         cDecl.Kind,
					Signature:    cSig,
					StartLine:    cDecl.StartLine,
					EndLine:      cDecl.EndLine,
					Reason:       fmt.Sprintf("Outgoing call %q from anchor %q", cDecl.Name, anchor.Name),
					Score:        90.0,
					Content:      cCode,
					TokenWeight:  cWeight,
					Hash:         cHash,
					Precision:    "precise",
					Relationship: "callee",
					Direction:    "outgoing",
				})
			}
		}
	}

	// Incoming references discovery
	refs, _, _ := s.registry.FindReferences(ctx, req.WorkspaceRoot, anchor.FilePath, anchor.Name)
	for _, ref := range refs {
		key := fmt.Sprintf("%s:%d", ref.FilePath, ref.StartLine)
		if seenItems[key] {
			continue
		}
		seenItems[key] = true

		relLower := strings.ToLower(ref.FilePath)
		isTest := strings.HasSuffix(relLower, "_test.go") || strings.Contains(relLower, ".test.") || strings.Contains(relLower, ".spec.")
		relType := ref.Relationship
		if relType == "" {
			if isTest {
				relType = "test"
			} else {
				relType = "caller"
			}
		}

		precision := ref.Precision
		if precision == "" {
			precision = "syntactic"
		}

		candidateItems = append(candidateItems, PackItem{
			FilePath:     ref.FilePath,
			SymbolName:   ref.SymbolName,
			Kind:         "reference",
			StartLine:    ref.StartLine,
			EndLine:      ref.EndLine,
			Reason:       fmt.Sprintf("Incoming reference from %s", ref.FilePath),
			Score:        80.0,
			Content:      ref.Content,
			TokenWeight:  EstimateTokens(ref.Content),
			Precision:    precision,
			Relationship: relType,
			Direction:    "incoming",
		})
	}

	// Redact candidates if privacy policy present
	if s.policyEngine != nil {
		redactor := dlp.NewRedactor()
		evalAnchor := s.policyEngine.EvaluateContent(anchorItem.Content)
		if !evalAnchor.Allowed {
			return nil, fmt.Errorf("anchor symbol %q content blocked by workspace privacy policy", anchor.Name)
		}
		redacted, mapping := redactor.Redact(anchorItem.Content)
		if len(mapping) > 0 {
			anchorItem.Content = redacted
			anchorItem.TokenWeight = EstimateTokens(redacted)
		}

		var filteredCandidates []PackItem
		for _, item := range candidateItems {
			ev := s.policyEngine.EvaluateContent(item.Content)
			if !ev.Allowed {
				continue
			}
			rText, rMap := redactor.Redact(item.Content)
			if len(rMap) > 0 {
				item.Content = rText
				item.TokenWeight = EstimateTokens(rText)
			}
			filteredCandidates = append(filteredCandidates, item)
		}
		candidateItems = filteredCandidates
	}

	// Sort candidates by score desc, path asc, line asc
	sort.SliceStable(candidateItems, func(i, j int) bool {
		if candidateItems[i].Score != candidateItems[j].Score {
			return candidateItems[i].Score > candidateItems[j].Score
		}
		if candidateItems[i].FilePath != candidateItems[j].FilePath {
			return candidateItems[i].FilePath < candidateItems[j].FilePath
		}
		return candidateItems[i].StartLine < candidateItems[j].StartLine
	})

	// Knapsack pack: reserve anchor first, then pack candidates
	var included []PackItem
	included = append(included, anchorItem)
	used := anchorItem.TokenWeight

	var truncatedManifest []string
	for _, c := range candidateItems {
		if used+c.TokenWeight <= req.Budget {
			included = append(included, c)
			used += c.TokenWeight
			continue
		}

		// Try degrading candidate to stub
		if c.Signature != "" && c.Signature != c.Content {
			stub := c
			stub.Content = stub.Signature + " { /* body omitted to fit budget */ }"
			stub.TokenWeight = EstimateTokens(stub.Content)
			if used+stub.TokenWeight <= req.Budget {
				included = append(included, stub)
				used += stub.TokenWeight
				continue
			}
		}

		truncatedManifest = append(truncatedManifest, c.FilePath)
	}

	pack := &ContextPack{
		Query:       fmt.Sprintf("symbol:%s", symbolName),
		Budget:      req.Budget,
		UsedTokens:  used,
		Items:       included,
		GeneratedAt: time.Now().UTC(),
		Coverage: &CoverageReport{
			TotalCandidates:    len(candidateItems) + 1,
			IncludedCandidates: len(included),
			TruncatedCount:     len(truncatedManifest),
			TruncatedManifest:  truncatedManifest,
		},
	}

	pack.Tokenizer = &TokenizerMetadata{
		Encoding:             tokenizer.EncodingDefault,
		VocabularyVersion:    "tiktoken-cl100k_base",
		Mode:                 tokenizer.ModeExact,
		ContentTokens:        used,
		SerializedPackTokens: EstimateTokens(pack.FormatMarkdown()),
	}

	return pack, nil
}

// FindSymbolDeclarations searches the workspace for declarations of symbolName.
func (s *ContextService) FindSymbolDeclarations(workspaceRoot, symbolName, fileFilter string) ([]Symbol, error) {
	if symbolName == "" {
		return nil, errors.New("symbol name cannot be empty")
	}

	var results []Symbol

	if fileFilter != "" {
		relPath := fileFilter
		if filepath.IsAbs(relPath) {
			rel, err := filepath.Rel(workspaceRoot, relPath)
			if err == nil {
				relPath = rel
			}
		}
		relPath = filepath.Clean(relPath)
		absPath := filepath.Join(workspaceRoot, relPath)

		src, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", fileFilter, err)
		}

		decls, err := extractDeclarationsFromAST(relPath, src)
		if err != nil {
			return nil, fmt.Errorf("failed to parse file %q: %w", fileFilter, err)
		}

		for _, d := range decls {
			if d.Name == symbolName {
				d.FilePath = relPath
				d.Workspace = workspaceRoot
				results = append(results, d)
			}
		}
		return results, nil
	}

	// Search across workspace files
	supportedExts := map[string]bool{
		".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".py": true, ".rs": true, ".java": true, ".c": true, ".cpp": true,
	}

	ignoredDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "target": true,
		".tzro": true, "dist": true, "build": true,
	}

	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}

		relPath, _ := filepath.Rel(workspaceRoot, path)
		if s.policyEngine != nil {
			eval := s.policyEngine.EvaluatePath(relPath)
			if !eval.Allowed {
				return nil
			}
		}

		content, rErr := os.ReadFile(path)
		if rErr != nil {
			return nil
		}

		// Fast filter: check if file text contains the symbol name before AST parsing
		if !strings.Contains(string(content), symbolName) {
			return nil
		}

		decls, pErr := extractDeclarationsFromAST(relPath, content)
		if pErr != nil {
			return nil
		}

		for _, decl := range decls {
			if decl.Name == symbolName {
				decl.FilePath = relPath
				decl.Workspace = workspaceRoot
				results = append(results, decl)
			}
		}
		return nil
	})

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].FilePath != results[j].FilePath {
			return results[i].FilePath < results[j].FilePath
		}
		return results[i].StartLine < results[j].StartLine
	})

	return results, nil
}
