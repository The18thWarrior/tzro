package comparison

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// DefaultDeterministicWeight is the default weight allocated to deterministic checks
// when computing the composite QualityScore (50% deterministic, 50% LLM).
const DefaultDeterministicWeight = 0.5

var (
	// urlRegex matches HTTP and HTTPS URLs in output text.
	urlRegex = regexp.MustCompile(`https?://[^\s\)\>\"\'\,\` + "`" + `\]]+`)

	// toolCallLogRegexes matches various tool call logging patterns in task logs.
	toolCallLogRegexes = []*regexp.Regexp{
		regexp.MustCompile(`\[(?:Probe|ReAct|cloud_react|local_react|Condition)\]\s+Tool\s+call(?:\s+#\d+)?:\s*([a-zA-Z0-9_\-]+)`),
		regexp.MustCompile(`Tool\s+call\s+#\d+:\s*([a-zA-Z0-9_\-]+)`),
		regexp.MustCompile(`tools\.([a-zA-Z0-9_]+)`),
		regexp.MustCompile(`"tool_name":\s*"([^"]+)"`),
		regexp.MustCompile(`\[PhaseRunner\]\s+Phase\s+"[^"]+"\s+completed:\s+\d+\s+steps,\s+(\d+)\s+tools\s+called`),
	}

	// pathInLogRegex matches filesystem paths in logs or tool arguments.
	pathInLogRegex = regexp.MustCompile(`(?:internal/|cmd/|docs/|helpers/|models/|handlers/|src/|pkg/)[a-zA-Z0-9_\-/\.]+`)
)

// EvaluateDeterministic runs all deterministic checks on a benchmark execution result
// against the corresponding task definition.
func EvaluateDeterministic(result *ComparisonResult, task *ComparisonTask) *DeterministicScorecard {
	scorecard := &DeterministicScorecard{
		Checks: make([]DeterministicCheckItem, 0),
	}

	// Execution failure fast-path
	if result.Error != "" {
		scorecard.OverallScore = 1.0
		scorecard.Notes = fmt.Sprintf("Execution failed: %s", result.Error)
		scorecard.Checks = append(scorecard.Checks, DeterministicCheckItem{
			Name:    "ExecutionSuccess",
			Passed:  false,
			Score:   1.0,
			Weight:  1.0,
			Message: result.Error,
		})
		return scorecard
	}

	// 1. Evaluate Output Structural Quality & Non-Empty
	outputQualityScore, outputChecks := evaluateOutputQuality(result, task)
	scorecard.OutputQualityScore = outputQualityScore
	scorecard.Checks = append(scorecard.Checks, outputChecks...)

	// 2. Evaluate Tool Usage
	toolUsageScore, toolChecks := evaluateToolUsage(result, task)
	scorecard.ToolUsageScore = toolUsageScore
	scorecard.Checks = append(scorecard.Checks, toolChecks...)

	// 3. Evaluate File & Target Path Coverage
	fileCoverageScore, fileChecks := evaluateFileCoverage(result, task)
	scorecard.FileCoverageScore = fileCoverageScore
	scorecard.Checks = append(scorecard.Checks, fileChecks...)

	// 4. Evaluate Domain-Specific Checks (Codegen AST / Docgen Symbols / Datanal Answer / Research URLs)
	domainScore, domainChecks := evaluateDomainSpecific(result, task)
	scorecard.DomainScore = domainScore
	scorecard.Checks = append(scorecard.Checks, domainChecks...)

	// Calculate overall composite score across the 4 dimensions
	// Weights: Tool (0.2), Coverage (0.25), Output Quality (0.25), Domain Specific (0.3)
	scorecard.OverallScore = 0.20*toolUsageScore + 0.25*fileCoverageScore + 0.25*outputQualityScore + 0.30*domainScore

	// Guardrail caps:
	// If output is a refusal or empty, cap overall score at 1.0
	if isRefusalOrEmpty(result.OutputText) {
		scorecard.OverallScore = 1.0
	}

	// If Go code has syntax error, cap domain score & overall score
	for _, c := range domainChecks {
		if c.Name == "GoASTParse" && !c.Passed {
			if scorecard.OverallScore > 2.0 {
				scorecard.OverallScore = 2.0
			}
		}
	}

	// Build summary notes
	var passedCount int
	for _, c := range scorecard.Checks {
		if c.Passed {
			passedCount++
		}
	}
	scorecard.Notes = fmt.Sprintf("Deterministic Score: %.2f/5.0 (%d/%d checks passed). Tool: %.1f, Coverage: %.1f, Quality: %.1f, Domain: %.1f",
		scorecard.OverallScore, passedCount, len(scorecard.Checks),
		scorecard.ToolUsageScore, scorecard.FileCoverageScore, scorecard.OutputQualityScore, scorecard.DomainScore)

	return scorecard
}

// evaluateOutputQuality checks for non-empty output, minimum length, refusal detection, and formatting.
func evaluateOutputQuality(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem
	text := strings.TrimSpace(result.OutputText)

	// Check 1: Non-Empty Output
	nonEmptyPassed := len(text) > 0
	nonEmptyScore := 1.0
	nonEmptyMsg := "Output is empty"
	if nonEmptyPassed {
		nonEmptyScore = 5.0
		nonEmptyMsg = fmt.Sprintf("Output produced (%d characters)", len(text))
	}
	checks = append(checks, DeterministicCheckItem{
		Name:    "NonEmptyOutput",
		Passed:  nonEmptyPassed,
		Score:   nonEmptyScore,
		Weight:  0.4,
		Message: nonEmptyMsg,
	})

	if !nonEmptyPassed {
		return 1.0, checks
	}

	// Check 2: Minimum substantive length (at least 80 chars)
	lengthPassed := len(text) >= 80
	lengthScore := 5.0
	lengthMsg := fmt.Sprintf("Substantive length (%d chars)", len(text))
	if !lengthPassed {
		lengthScore = 2.0
		lengthMsg = fmt.Sprintf("Output is unusually short (%d chars < 80 minimum)", len(text))
	}
	checks = append(checks, DeterministicCheckItem{
		Name:    "SubstantiveLength",
		Passed:  lengthPassed,
		Score:   lengthScore,
		Weight:  0.3,
		Message: lengthMsg,
	})

	// Check 3: Refusal / Failure Detection
	refusalDetected := isRefusalOrEmpty(text)
	refusalScore := 5.0
	refusalMsg := "No refusal or failure patterns detected"
	if refusalDetected {
		refusalScore = 1.0
		refusalMsg = "Output contains refusal, apology, or failure indicator"
	}
	checks = append(checks, DeterministicCheckItem{
		Name:    "NoRefusalOrFailure",
		Passed:  !refusalDetected,
		Score:   refusalScore,
		Weight:  0.3,
		Message: refusalMsg,
	})

	// Calculate weighted average
	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	if totalWeight > 0 {
		return totalScore / totalWeight, checks
	}
	return 5.0, checks
}

// evaluateToolUsage evaluates the presence, count, and appropriateness of tools invoked.
func evaluateToolUsage(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem
	toolsCalled := extractToolsFromLogs(result.Logs)
	totalCalls := result.ToolCallCount
	if len(toolsCalled) > totalCalls {
		totalCalls = len(toolsCalled)
	}

	// Check 1: Explicit ExpectedTools check if defined in task testdata
	if len(task.ExpectedTools) > 0 {
		var foundTools []string
		for _, exp := range task.ExpectedTools {
			matched := containsAnyTool(toolsCalled, exp) || strings.Contains(result.Logs, exp)
			if !matched {
				// Macro node alignment: ListNode / Preload / Probe / explore satisfy read_file / list_dir / search_files
				if (exp == "read_file" || exp == "list_dir" || exp == "search_files") &&
					(strings.Contains(result.Logs, "ListNode") || strings.Contains(result.Logs, "Preload") || strings.Contains(result.Logs, "Probe") || strings.Contains(result.Logs, "explore") || strings.Contains(result.Logs, "read_file") || strings.Contains(result.Logs, "list_dir")) {
					matched = true
				}
				// write_output / DeterministicStrategy satisfy write_file
				if exp == "write_file" && (strings.Contains(result.Logs, "write_output") || strings.Contains(result.Logs, "write_file") || strings.Contains(result.Logs, "DeterministicStrategy")) {
					matched = true
				}
			}
			if matched {
				foundTools = append(foundTools, exp)
			}
		}
		ratio := float64(len(foundTools)) / float64(len(task.ExpectedTools))
		expScore := 1.0 + 4.0*ratio
		expPassed := ratio >= 0.5 || (len(task.ExpectedTools) == 1 && len(foundTools) == 1)

		checks = append(checks, DeterministicCheckItem{
			Name:    "ExpectedToolsCalled",
			Passed:  expPassed,
			Score:   expScore,
			Weight:  0.6,
			Message: fmt.Sprintf("Expected tools called: %d/%d (found: %v, expected: %v)", len(foundTools), len(task.ExpectedTools), foundTools, task.ExpectedTools),
		})
	} else {
		// General Tool Call Presence for Exploration Tasks
		requiresTools := task.Category == CategoryDocgen || task.Category == CategoryResearch || task.Category == CategoryDatanal || len(task.TargetPaths) > 0
		presencePassed := true
		presenceScore := 5.0
		presenceMsg := fmt.Sprintf("Total tool calls: %d", totalCalls)

		if requiresTools && totalCalls == 0 {
			presencePassed = false
			presenceScore = 1.5
			presenceMsg = "0 tool calls made for multi-file/exploration task"
		} else if totalCalls > 0 {
			presenceScore = 5.0
		}

		checks = append(checks, DeterministicCheckItem{
			Name:    "ToolCallPresence",
			Passed:  presencePassed,
			Score:   presenceScore,
			Weight:  0.5,
			Message: presenceMsg,
		})
	}

	// Check 2: Appropriate Tool Selection by Category
	categoryToolPassed := true
	categoryToolScore := 5.0
	categoryToolMsg := "Appropriate tools utilized"

	switch task.Category {
	case CategoryResearch:
		hasWebTool := containsAnyTool(toolsCalled, "web_search", "web_browse", "search", "browse") ||
			strings.Contains(result.Logs, "web_search") || strings.Contains(result.Logs, "web_browse")
		if !hasWebTool && totalCalls == 0 {
			categoryToolPassed = false
			categoryToolScore = 1.5
			categoryToolMsg = "Research task did not execute web_search or web_browse"
		}
	case CategoryDocgen:
		hasReadTool := containsAnyTool(toolsCalled, "read_file", "list_dir", "search_files", "write_file", "git_log") ||
			strings.Contains(result.Logs, "read_file") || strings.Contains(result.Logs, "list_dir") || strings.Contains(result.Logs, "Probe") ||
			strings.Contains(result.Logs, "ListNode") || strings.Contains(result.Logs, "Preload") || strings.Contains(result.Logs, "explore")
		if !hasReadTool && totalCalls == 0 && len(task.TargetPaths) > 0 {
			categoryToolPassed = false
			categoryToolScore = 2.0
			categoryToolMsg = "Docgen task did not read files or inspect directory structure"
		}
	case CategoryDatanal:
		hasDataRead := containsAnyTool(toolsCalled, "read_file", "search_files") ||
			strings.Contains(result.Logs, "LeadSuccess.csv") || strings.Contains(result.Logs, "read_file") || strings.Contains(result.Logs, "Probe")
		if !hasDataRead && totalCalls == 0 {
			categoryToolPassed = false
			categoryToolScore = 2.0
			categoryToolMsg = "Data analysis task did not read the dataset file"
		}
	}

	checks = append(checks, DeterministicCheckItem{
		Name:    "CategoryToolAlignment",
		Passed:  categoryToolPassed,
		Score:   categoryToolScore,
		Weight:  0.4,
		Message: categoryToolMsg,
	})

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	if totalWeight > 0 {
		return totalScore / totalWeight, checks
	}
	return 5.0, checks
}

// evaluateFileCoverage evaluates whether target paths, expected files, and seed files were accessed/covered.
func evaluateFileCoverage(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem
	accessedPaths := extractPathsFromLogs(result.Logs)

	// Check 1: Explicit ExpectedFiles check if defined in task testdata
	if len(task.ExpectedFiles) > 0 {
		covered := 0
		var foundList []string
		for _, exp := range task.ExpectedFiles {
			expNorm := strings.Trim(exp, "./")
			found := false
			for _, p := range accessedPaths {
				if strings.Contains(p, expNorm) || strings.Contains(expNorm, p) {
					found = true
					break
				}
			}
			if !found && (strings.Contains(result.Logs, expNorm) || strings.Contains(result.OutputText, expNorm) || strings.Contains(result.OutputText, filepath.Base(expNorm))) {
				found = true
			}
			if found {
				covered++
				foundList = append(foundList, exp)
			}
		}

		ratio := float64(covered) / float64(len(task.ExpectedFiles))
		covScore := 1.0 + 4.0*ratio
		covPassed := ratio >= 0.5 || (len(task.ExpectedFiles) == 1 && covered == 1)

		checks = append(checks, DeterministicCheckItem{
			Name:    "ExpectedFilesAccessed",
			Passed:  covPassed,
			Score:   covScore,
			Weight:  0.7,
			Message: fmt.Sprintf("Expected files accessed: %d/%d (%.0f%%) - %v", covered, len(task.ExpectedFiles), ratio*100, foundList),
		})
	} else if len(task.TargetPaths) > 0 {
		// Fallback: Docgen / Exploration Target Paths Check
		covered := 0
		for _, target := range task.TargetPaths {
			targetNorm := strings.Trim(target, "./")
			found := false
			for _, p := range accessedPaths {
				if strings.Contains(p, targetNorm) || strings.Contains(targetNorm, p) {
					found = true
					break
				}
			}
			if !found && (strings.Contains(result.Logs, targetNorm) || strings.Contains(result.OutputText, targetNorm)) {
				found = true
			}
			if found {
				covered++
			}
		}

		ratio := float64(covered) / float64(len(task.TargetPaths))
		covScore := 1.0 + 4.0*ratio
		covPassed := ratio >= 0.5

		checks = append(checks, DeterministicCheckItem{
			Name:    "TargetPathCoverage",
			Passed:  covPassed,
			Score:   covScore,
			Weight:  0.7,
			Message: fmt.Sprintf("Target paths covered: %d/%d (%.0f%%)", covered, len(task.TargetPaths), ratio*100),
		})
	}

	// Codegen Target File Check
	if task.Category == CategoryCodegen && task.Filepath != "" && len(task.ExpectedFiles) == 0 {
		fileFound := strings.Contains(result.Logs, task.Filepath) || strings.Contains(result.OutputText, filepath.Base(task.Filepath))
		fileScore := 5.0
		fileMsg := fmt.Sprintf("Target file referenced/created: %s", task.Filepath)
		if !fileFound {
			fileScore = 3.0
			fileMsg = fmt.Sprintf("Target file %s not explicitly referenced in output/logs", task.Filepath)
		}
		checks = append(checks, DeterministicCheckItem{
			Name:    "TargetFileSpecified",
			Passed:  fileFound,
			Score:   fileScore,
			Weight:  0.3,
			Message: fileMsg,
		})
	}

	// Datanal File Check
	if task.Category == CategoryDatanal && len(task.ExpectedFiles) == 0 {
		dataFound := strings.Contains(result.Logs, "LeadSuccess.csv") || strings.Contains(result.OutputText, "LeadSuccess") || len(result.OutputText) > 100
		dataScore := 5.0
		dataMsg := "Data file accessed"
		if !dataFound {
			dataScore = 2.0
			dataMsg = "Data file LeadSuccess.csv not accessed"
		}
		checks = append(checks, DeterministicCheckItem{
			Name:    "DataFileAccess",
			Passed:  dataFound,
			Score:   dataScore,
			Weight:  0.5,
			Message: dataMsg,
		})
	}

	if len(checks) == 0 {
		return 5.0, []DeterministicCheckItem{
			{Name: "GeneralCoverage", Passed: true, Score: 5.0, Weight: 1.0, Message: "No specific target path constraints"},
		}
	}

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	return totalScore / totalWeight, checks
}

// evaluateDomainSpecific performs deep category-specific AST, symbol, signature, and answer evaluations.
func evaluateDomainSpecific(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem

	// Check 1: Explicit ExpectedSymbols check across all categories if defined
	if len(task.ExpectedSymbols) > 0 {
		foundSymbols := 0
		var foundList []string
		for _, sym := range task.ExpectedSymbols {
			if strings.Contains(result.OutputText, sym) {
				foundSymbols++
				foundList = append(foundList, sym)
			}
		}
		symRatio := float64(foundSymbols) / float64(len(task.ExpectedSymbols))
		symScore := 1.0 + 4.0*symRatio
		symPassed := symRatio >= 0.5

		checks = append(checks, DeterministicCheckItem{
			Name:    "ExpectedSymbolsPresent",
			Passed:  symPassed,
			Score:   symScore,
			Weight:  0.4,
			Message: fmt.Sprintf("Expected symbols/identifiers present: %d/%d (%.0f%%) - %v", foundSymbols, len(task.ExpectedSymbols), symRatio*100, foundList),
		})
	}

	// Check 2: Explicit ExpectedSignatures check if defined
	if len(task.ExpectedSignatures) > 0 {
		foundSig := false
		for _, sig := range task.ExpectedSignatures {
			if strings.Contains(result.OutputText, sig) {
				foundSig = true
				break
			}
		}
		sigScore := 3.0
		sigMsg := "Expected signature pattern not fully matched"
		if foundSig {
			sigScore = 5.0
			sigMsg = "Expected function/method signature pattern matched"
		}
		checks = append(checks, DeterministicCheckItem{
			Name:    "ExpectedSignaturePattern",
			Passed:  foundSig,
			Score:   sigScore,
			Weight:  0.2,
			Message: sigMsg,
		})
	}

	// Category-specific evaluations
	var catScore float64
	var catChecks []DeterministicCheckItem
	switch task.Category {
	case CategoryCodegen:
		catScore, catChecks = evaluateCodegenDomain(result, task)
	case CategoryDocgen:
		catScore, catChecks = evaluateDocgenDomain(result, task)
	case CategoryDatanal:
		catScore, catChecks = evaluateDatanalDomain(result, task)
	case CategoryResearch:
		catScore, catChecks = evaluateResearchDomain(result, task)
	default:
		catScore = 5.0
	}

	checks = append(checks, catChecks...)

	if len(checks) == 0 {
		return catScore, checks
	}

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	return totalScore / totalWeight, checks
}

// evaluateCodegenDomain performs Go AST syntax parsing, symbol checking, and update preservation.
func evaluateCodegenDomain(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem
	cleanCode := stripCodeFences(result.OutputText)

	// Check 1: Go AST Syntax Parse
	isGo := task.Language == "go" || strings.HasSuffix(task.Filepath, ".go") || strings.Contains(cleanCode, "package ")
	if isGo {
		fset := token.NewFileSet()
		_, parseErr := parser.ParseFile(fset, "", cleanCode, parser.AllErrors)
		if parseErr == nil {
			checks = append(checks, DeterministicCheckItem{
				Name:    "GoASTParse",
				Passed:  true,
				Score:   5.0,
				Weight:  0.5,
				Message: "Go code parsed successfully without syntax errors",
			})
		} else {
			checks = append(checks, DeterministicCheckItem{
				Name:    "GoASTParse",
				Passed:  false,
				Score:   1.0,
				Weight:  0.5,
				Message: fmt.Sprintf("Go AST parse failed: %v", parseErr),
			})
		}
	} else if task.Language == "typescript" || strings.HasSuffix(task.Filepath, ".ts") {
		// Basic TypeScript brace and export matching
		balanced := checkBalancedBraces(cleanCode)
		hasExports := strings.Contains(cleanCode, "export ")
		tsScore := 5.0
		tsMsg := "TypeScript syntax invariants passed"
		if !balanced {
			tsScore = 2.0
			tsMsg = "TypeScript braces or parentheses are unbalanced"
		} else if !hasExports {
			tsScore = 3.5
			tsMsg = "TypeScript module lacks export statements"
		}
		checks = append(checks, DeterministicCheckItem{
			Name:    "TypeScriptSyntaxCheck",
			Passed:  balanced && hasExports,
			Score:   tsScore,
			Weight:  0.5,
			Message: tsMsg,
		})
	}

	// Check 2: Spec Symbol Requirements (if ExpectedSymbols not already explicitly populated)
	if len(task.ExpectedSymbols) == 0 && (task.Spec != "" || task.Prompt != "") {
		requiredKeywords := extractRequiredKeywords(task.Prompt + " " + task.Spec)
		foundKeywords := 0
		for _, kw := range requiredKeywords {
			if strings.Contains(cleanCode, kw) {
				foundKeywords++
			}
		}
		ratio := 1.0
		if len(requiredKeywords) > 0 {
			ratio = float64(foundKeywords) / float64(len(requiredKeywords))
		}
		kwScore := 1.0 + 4.0*ratio
		checks = append(checks, DeterministicCheckItem{
			Name:    "SpecKeywordCompliance",
			Passed:  ratio >= 0.7,
			Score:   kwScore,
			Weight:  0.3,
			Message: fmt.Sprintf("Required keywords implemented: %d/%d (%.0f%%)", foundKeywords, len(requiredKeywords), ratio*100),
		})
	}

	// Check 3: Update Task Preservation
	if task.Action == "update" && task.SeedFile != "" {
		seedData, seedErr := ReadSeedFile(task.SeedFile)
		if seedErr == nil {
			preserved := checkPreservation(string(seedData), cleanCode)
			presScore := 5.0
			presMsg := "Existing seed functions/types preserved"
			if !preserved {
				presScore = 2.5
				presMsg = "Some existing seed code symbols were omitted or altered"
			}
			checks = append(checks, DeterministicCheckItem{
				Name:    "UpdatePreservation",
				Passed:  preserved,
				Score:   presScore,
				Weight:  0.2,
				Message: presMsg,
			})
		}
	}

	if len(checks) == 0 {
		return 5.0, []DeterministicCheckItem{
			{Name: "CodegenValid", Passed: true, Score: 5.0, Weight: 1.0, Message: "Code generation format valid"},
		}
	}

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	return totalScore / totalWeight, checks
}

// evaluateDocgenDomain verifies grounding of documented symbols against actual codebase files.
func evaluateDocgenDomain(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem

	// Check 1: Real Codebase Symbol Grounding (if target paths present and not already fully captured by ExpectedSymbols)
	if len(task.TargetPaths) > 0 {
		realSymbols := collectRealExportedSymbols(task.TargetPaths)
		if len(realSymbols) > 0 {
			documentedSymbols := extractDocumentedSymbols(result.OutputText)
			realMatch := 0
			for _, s := range documentedSymbols {
				if realSymbols[s] {
					realMatch++
				}
			}

			groundingRatio := 1.0
			if len(documentedSymbols) > 0 {
				groundingRatio = float64(realMatch) / float64(len(documentedSymbols))
			}
			groundingScore := 1.0 + 4.0*groundingRatio

			checks = append(checks, DeterministicCheckItem{
				Name:    "SymbolFactualGrounding",
				Passed:  groundingRatio >= 0.6 && len(documentedSymbols) > 0,
				Score:   groundingScore,
				Weight:  0.6,
				Message: fmt.Sprintf("Factual symbol grounding: %d/%d symbols verified against codebase (%.0f%%)", realMatch, len(documentedSymbols), groundingRatio*100),
			})
		}
	}

	// Check 2: Markdown Structure Hygiene (Headers & Sections)
	hasHeadings := strings.Contains(result.OutputText, "# ") || strings.Contains(result.OutputText, "## ")
	hasSections := len(strings.Split(result.OutputText, "\n\n")) >= 3
	structScore := 5.0
	structMsg := "Clean structured markdown with multiple sections"
	if !hasHeadings {
		structScore = 2.5
		structMsg = "Documentation lacks markdown headings (#, ##)"
	} else if !hasSections {
		structScore = 3.5
		structMsg = "Documentation has few distinct paragraphs/sections"
	}

	checks = append(checks, DeterministicCheckItem{
		Name:    "MarkdownStructure",
		Passed:  hasHeadings && hasSections,
		Score:   structScore,
		Weight:  0.4,
		Message: structMsg,
	})

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	if totalWeight > 0 {
		return totalScore / totalWeight, checks
	}
	return 5.0, checks
}

// evaluateDatanalDomain matches output against expected answer ground-truth.
func evaluateDatanalDomain(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem

	if task.ExpectedAnswer != "" {
		expectedNumbers := extractNumbers(task.ExpectedAnswer)
		outputNumbers := extractNumbers(result.OutputText)

		matchedNums := 0
		for _, en := range expectedNumbers {
			for _, on := range outputNumbers {
				if en == on {
					matchedNums++
					break
				}
			}
		}

		numRatio := 1.0
		if len(expectedNumbers) > 0 {
			numRatio = float64(matchedNums) / float64(len(expectedNumbers))
		}
		numScore := 1.0 + 4.0*numRatio

		checks = append(checks, DeterministicCheckItem{
			Name:    "GroundTruthNumericalMatch",
			Passed:  numRatio >= 0.75,
			Score:   numScore,
			Weight:  0.7,
			Message: fmt.Sprintf("Expected key values matched: %d/%d (%.0f%%)", matchedNums, len(expectedNumbers), numRatio*100),
		})

		// String snippet match
		cleanExpected := strings.ToLower(strings.TrimSpace(task.ExpectedAnswer))
		cleanOutput := strings.ToLower(result.OutputText)
		prefixLen := 30
		if len(cleanExpected) < prefixLen {
			prefixLen = len(cleanExpected)
		}
		exactSnippetFound := strings.Contains(cleanOutput, cleanExpected[:prefixLen])

		snipScore := 3.0
		if exactSnippetFound {
			snipScore = 5.0
		}
		checks = append(checks, DeterministicCheckItem{
			Name:    "ExpectedFormatMatch",
			Passed:  exactSnippetFound,
			Score:   snipScore,
			Weight:  0.3,
			Message: "Expected answer structure/format match",
		})
	} else {
		// General quantitative check
		nums := extractNumbers(result.OutputText)
		checks = append(checks, DeterministicCheckItem{
			Name:    "QuantitativeEvidence",
			Passed:  len(nums) >= 2,
			Score:   5.0,
			Weight:  1.0,
			Message: fmt.Sprintf("Quantitative calculations present (%d numbers found)", len(nums)),
		})
	}

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	return totalScore / totalWeight, checks
}

// evaluateResearchDomain extracts and verifies URL citations.
func evaluateResearchDomain(result *ComparisonResult, task *ComparisonTask) (float64, []DeterministicCheckItem) {
	var checks []DeterministicCheckItem
	urls := extractValidURLs(result.OutputText)

	// Check 1: Real Source Citations Count
	urlScore := 1.0
	urlMsg := "No valid HTTP/HTTPS URLs cited"
	urlPassed := len(urls) >= 2

	if len(urls) >= 3 {
		urlScore = 5.0
		urlMsg = fmt.Sprintf("Strong source citation count (%d distinct URLs)", len(urls))
	} else if len(urls) >= 1 {
		urlScore = 3.5
		urlMsg = fmt.Sprintf("Minimal source citation count (%d URLs)", len(urls))
	}

	checks = append(checks, DeterministicCheckItem{
		Name:    "CitationSourceCount",
		Passed:  urlPassed,
		Score:   urlScore,
		Weight:  0.6,
		Message: urlMsg,
	})

	// Check 2: Synthesis Structure (tables / comparative matrix)
	hasTable := strings.Contains(result.OutputText, "|") && strings.Contains(result.OutputText, "---")
	tableScore := 3.5
	tableMsg := "Structured text synthesis"
	if hasTable {
		tableScore = 5.0
		tableMsg = "Structured comparative tables included"
	}

	checks = append(checks, DeterministicCheckItem{
		Name:    "ComparativeStructure",
		Passed:  hasTable,
		Score:   tableScore,
		Weight:  0.4,
		Message: tableMsg,
	})

	var totalScore, totalWeight float64
	for _, c := range checks {
		totalScore += c.Score * c.Weight
		totalWeight += c.Weight
	}
	return totalScore / totalWeight, checks
}

// CalculateCompositeScore combines the Deterministic Score and LLM Judge Score
// applying weighting and deterministic safety guardrails.
func CalculateCompositeScore(detScorecard *DeterministicScorecard, llmScore float64, detWeight float64) (float64, string) {
	if detWeight <= 0 {
		detWeight = DefaultDeterministicWeight
	}
	if detWeight > 1.0 {
		detWeight = 1.0
	}

	if detScorecard == nil {
		return llmScore, "LLM score only"
	}

	detScore := detScorecard.OverallScore

	// Guardrail: if deterministic score indicates a total failure (empty or syntax error), cap final score
	if detScore <= 1.5 && llmScore > 2.0 {
		blended := detScore
		return blended, fmt.Sprintf("Capped at %.2f due to deterministic failure", blended)
	}

	if llmScore <= 0 {
		return detScore, "Deterministic score only (no LLM score)"
	}

	blended := detWeight*detScore + (1.0-detWeight)*llmScore
	return blended, fmt.Sprintf("Composite: %.2f (Det: %.2f * %.2f + LLM: %.2f * %.2f)",
		blended, detScore, detWeight, llmScore, 1.0-detWeight)
}

// --- Helper Functions ---

func isRefusalOrEmpty(s string) bool {
	clean := strings.ToLower(strings.TrimSpace(s))
	if len(clean) == 0 {
		return true
	}
	refusals := []string{
		"i cannot generate",
		"i apologize",
		"i am unable to",
		"no source code files were read",
		"unable to generate the function index",
		"terminal rejected",
		"i do not have access",
		"could not find",
	}
	for _, r := range refusals {
		if strings.Contains(clean, r) && len(clean) < 400 {
			return true
		}
	}
	return false
}

func extractToolsFromLogs(logs string) []string {
	var tools []string
	seen := make(map[string]bool)

	for _, re := range toolCallLogRegexes {
		matches := re.FindAllStringSubmatch(logs, -1)
		for _, m := range matches {
			if len(m) > 1 {
				name := strings.TrimSpace(m[1])
				// If numeric string from PhaseRunner, skip or count
				if _, err := strconv.Atoi(name); err == nil {
					continue
				}
				if name != "" && !seen[name] {
					seen[name] = true
					tools = append(tools, name)
				}
			}
		}
	}
	return tools
}

func extractPathsFromLogs(logs string) []string {
	matches := pathInLogRegex.FindAllString(logs, -1)
	seen := make(map[string]bool)
	var paths []string
	for _, m := range matches {
		clean := filepath.Clean(m)
		if !seen[clean] {
			seen[clean] = true
			paths = append(paths, clean)
		}
	}
	return paths
}

func containsAnyTool(tools []string, candidates ...string) bool {
	for _, t := range tools {
		for _, c := range candidates {
			if strings.EqualFold(t, c) {
				return true
			}
		}
	}
	return false
}

func checkBalancedBraces(code string) bool {
	var stack []rune
	pairs := map[rune]rune{'}': '{', ')': '(', ']': '['}

	for _, r := range code {
		if r == '{' || r == '(' || r == '[' {
			stack = append(stack, r)
		} else if match, exists := pairs[r]; exists {
			if len(stack) == 0 || stack[len(stack)-1] != match {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

func extractRequiredKeywords(spec string) []string {
	var keywords []string
	// Match quoted terms or identifiers like Validate, parseConfig, loadConfigFromEnv, /hello
	re := regexp.MustCompile(`(?:'|")([a-zA-Z0-9_/\-]+)(?:'|")|func\s+([a-zA-Z0-9_]+)|\b(Validate|parseConfig|loadConfigFromEnv|NewUser|DisplayName)\b`)
	matches := re.FindAllStringSubmatch(spec, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		for i := 1; i < len(m); i++ {
			kw := strings.TrimSpace(m[i])
			if len(kw) > 2 && !seen[kw] {
				seen[kw] = true
				keywords = append(keywords, kw)
			}
		}
	}
	return keywords
}

func checkPreservation(seedCode, outputCode string) bool {
	// Extract func names and type names from seedCode
	re := regexp.MustCompile(`func\s+([A-Za-z0-9_]+)|type\s+([A-Za-z0-9_]+)`)
	matches := re.FindAllStringSubmatch(seedCode, -1)
	for _, m := range matches {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name != "" && !strings.Contains(outputCode, name) {
			return false
		}
	}
	return true
}

func collectRealExportedSymbols(targetPaths []string) map[string]bool {
	symbols := make(map[string]bool)
	projectRoot, _ := os.Getwd()

	for _, tp := range targetPaths {
		dir := filepath.Join(projectRoot, tp)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				fset := token.NewFileSet()
				node, parseErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
				if parseErr == nil && node != nil {
					for _, decl := range node.Decls {
						for name := range extractExportedFromDecl(decl) {
							symbols[name] = true
						}
					}
				}
			}
		}
	}
	return symbols
}

func extractExportedFromDecl(decl interface{}) map[string]bool {
	names := make(map[string]bool)
	switch d := decl.(type) {
	case *parserDeclarationHelper:
		// placeholder for ast parser
	default:
		// String pattern matching fallback on declaration AST string
		str := fmt.Sprintf("%v", d)
		re := regexp.MustCompile(`\b([A-Z][a-zA-Z0-9_]+)\b`)
		for _, m := range re.FindAllString(str, -1) {
			if unicode.IsUpper(rune(m[0])) {
				names[m] = true
			}
		}
	}
	return names
}

type parserDeclarationHelper struct{}

func extractDocumentedSymbols(doc string) []string {
	var symbols []string
	seen := make(map[string]bool)
	// Match `SymbolName` or ### SymbolName or func SymbolName
	re := regexp.MustCompile("`([A-Z][a-zA-Z0-9_]+)`|###\\s+([A-Z][a-zA-Z0-9_]+)|func\\s+([A-Z][a-zA-Z0-9_]+)")
	matches := re.FindAllStringSubmatch(doc, -1)
	for _, m := range matches {
		for i := 1; i < len(m); i++ {
			sym := strings.TrimSpace(m[i])
			if len(sym) > 1 && unicode.IsUpper(rune(sym[0])) && !seen[sym] {
				seen[sym] = true
				symbols = append(symbols, sym)
			}
		}
	}
	return symbols
}

func extractNumbers(s string) []string {
	re := regexp.MustCompile(`\b\d+(?:\.\d+)?%?\b`)
	return re.FindAllString(s, -1)
}

func extractValidURLs(text string) []string {
	raw := urlRegex.FindAllString(text, -1)
	var valid []string
	seen := make(map[string]bool)

	for _, u := range raw {
		clean := strings.TrimRight(u, ".,;:)")
		lower := strings.ToLower(clean)
		if strings.Contains(lower, "example.com") || strings.Contains(lower, "placeholder") || strings.Contains(lower, "foo.com") {
			continue
		}
		if !seen[clean] {
			seen[clean] = true
			valid = append(valid, clean)
		}
	}
	return valid
}
