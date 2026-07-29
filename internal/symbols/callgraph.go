package symbols

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	_ "modernc.org/sqlite"
)

// CallEdge represents a function call relationship between two symbols.
type CallEdge struct {
	CallerName string `json:"callerName"` // Name of the calling function
	CalleeName string `json:"calleeName"` // Name of the called function
	CallerFile string `json:"callerFile"` // File containing the caller
	CalleeFile string `json:"calleeFile"` // File containing the callee
	CallLine   int    `json:"callLine"`   // Line number of the call site
	EdgeKind   string `json:"edgeKind"`   // "direct", "method", "interface"
}

// CallGraphSymbol extends Symbol with a content hash for staleness detection.
type CallGraphSymbol struct {
	Symbol
	ContentHash string `json:"contentHash"` // SHA-256 of the file content
}

// buildSymbolTable creates a name→symbol lookup from a slice of symbols.
func buildSymbolTable(symbols []Symbol) map[string]Symbol {
	table := make(map[string]Symbol)
	for _, s := range symbols {
		// Use file:name as key to handle same-name across files
		key := s.File + ":" + s.Name
		table[key] = s
		// Also store by name alone for cross-file resolution
		table[s.Name] = s
	}
	return table
}

// ExtractCallEdges walks function bodies in source looking for call expressions
// and resolves them against the symbol table. Returns edges for calls that
// resolve to known symbols.
func ExtractCallEdges(symTable map[string]Symbol, filename string, source []byte) []CallEdge {
	if len(source) == 0 {
		return nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry == nil {
		return nil
	}
	lang := entry.Language()
	if lang == nil {
		return nil
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return nil
	}

	langName := strings.ToLower(entry.Name)
	switch langName {
	case "go":
		return extractGoCallEdges(bt, root, source, filename, symTable)
	default:
		return nil // Other languages can be added later
	}
}

// extractGoCallEdges walks Go function/method bodies for call_expression nodes.
func extractGoCallEdges(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string, symTable map[string]Symbol) []CallEdge {
	var edges []CallEdge

	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		if nodeType != "function_declaration" && nodeType != "method_declaration" {
			return
		}

		nameNode := bt.ChildByField(node, "name")
		if nameNode == nil {
			return
		}
		callerName := bt.NodeText(nameNode)

		// Walk the function body for call expressions
		body := bt.ChildByField(node, "body")
		if body == nil {
			return
		}

		walkCallExpressions(bt, body, source, func(calleeName string, callLine int, edgeKind string) {
			// Check if callee exists in symbol table
			// Prefer same-file resolution
			if _, ok := symTable[filename+":"+calleeName]; ok {
				edges = append(edges, CallEdge{
					CallerName: callerName,
					CalleeName: calleeName,
					CallerFile: filename,
					CalleeFile: filename,
					CallLine:   callLine,
					EdgeKind:   edgeKind,
				})
			} else if sym, ok := symTable[calleeName]; ok {
				edges = append(edges, CallEdge{
					CallerName: callerName,
					CalleeName: calleeName,
					CallerFile: filename,
					CalleeFile: sym.File,
					CallLine:   callLine,
					EdgeKind:   edgeKind,
				})
			}
		})
	})

	return edges
}

// walkCallExpressions recursively walks AST nodes looking for call expressions.
func walkCallExpressions(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, fn func(calleeName string, callLine int, edgeKind string)) {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}

		nodeType := bt.NodeType(child)
		if nodeType == "call_expression" {
			funcNode := bt.ChildByField(child, "function")
			if funcNode != nil {
				funcType := bt.NodeType(funcNode)
				calleeName := bt.NodeText(funcNode)
				callLine := int(child.StartPoint().Row) + 1
				edgeKind := "direct"

				switch funcType {
				case "identifier":
					// Direct function call: foo()
					fn(calleeName, callLine, edgeKind)
				case "selector_expression":
					// Method call: s.foo() or pkg.Foo()
					fieldNode := bt.ChildByField(funcNode, "field")
					if fieldNode != nil {
						calleeName = bt.NodeText(fieldNode)
						edgeKind = "method"
						fn(calleeName, callLine, edgeKind)
					}
				}
			}
		}

		// Recurse into children
		walkCallExpressions(bt, child, source, fn)
	}
}

// BuildCallGraph walks a directory, extracts all symbols and call edges.
// Pass 1: ExtractAllSymbols on each file → builds symbol lookup table
// Pass 2: ExtractCallEdges for each file → builds edge list
func BuildCallGraph(dir string) ([]CallGraphSymbol, []CallEdge, error) {
	var allSymbols []CallGraphSymbol
	var allEdges []CallEdge
	fileContents := make(map[string][]byte)

	// Pass 1: Extract all symbols
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip test files and non-code files
		name := d.Name()
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if !isParseableFile(name) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		relPath, _ := filepath.Rel(dir, path)
		if relPath == "" {
			relPath = name
		}

		fileContents[relPath] = content

		symbols, err := ExtractAllSymbols(name, content)
		if err != nil {
			return nil // skip parse errors
		}

		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:])

		for _, sym := range symbols {
			sym.File = relPath
			allSymbols = append(allSymbols, CallGraphSymbol{
				Symbol:      sym,
				ContentHash: hashStr,
			})
		}

		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walking directory %s: %w", dir, err)
	}

	// Build symbol table from all symbols
	symTable := make(map[string]Symbol)
	for _, cs := range allSymbols {
		key := cs.File + ":" + cs.Name
		symTable[key] = cs.Symbol
		symTable[cs.Name] = cs.Symbol
	}

	// Pass 2: Extract call edges
	for relPath, content := range fileContents {
		edges := ExtractCallEdges(symTable, relPath, content)
		allEdges = append(allEdges, edges...)
	}

	return allSymbols, allEdges, nil
}

// isParseableFile checks if a file has a supported code extension.
func isParseableFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java":
		return true
	}
	return false
}

// --- CallGraphStore: SQLite persistence ---

// CallGraphStore persists call graph data to SQLite.
type CallGraphStore struct {
	db *sql.DB
}

// NewCallGraphStore creates a new store, creating the database and tables if needed.
func NewCallGraphStore(dbPath string) (*CallGraphStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	store := &CallGraphStore{db: db}
	if err := store.ensureTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating tables: %w", err)
	}

	return store, nil
}

// Close closes the underlying database connection.
func (s *CallGraphStore) Close() error {
	return s.db.Close()
}

func (s *CallGraphStore) ensureTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS call_graph_symbols (
			dir TEXT NOT NULL,
			file TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			signature TEXT,
			doc_comment TEXT,
			line INTEGER,
			end_line INTEGER,
			exported INTEGER,
			content_hash TEXT,
			PRIMARY KEY (dir, file, name)
		)`,
		`CREATE TABLE IF NOT EXISTS call_graph_edges (
			dir TEXT NOT NULL,
			caller_name TEXT NOT NULL,
			callee_name TEXT NOT NULL,
			caller_file TEXT NOT NULL,
			callee_file TEXT NOT NULL,
			call_line INTEGER,
			edge_kind TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cg_symbols_dir ON call_graph_symbols(dir)`,
		`CREATE INDEX IF NOT EXISTS idx_cg_edges_dir ON call_graph_edges(dir)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", q[:50], err)
		}
	}
	return nil
}

// SaveGraph persists symbols and edges for a directory, replacing any existing data.
func (s *CallGraphStore) SaveGraph(dir string, symbols []CallGraphSymbol, edges []CallEdge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing data for this directory
	if _, err := tx.Exec("DELETE FROM call_graph_symbols WHERE dir = ?", dir); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM call_graph_edges WHERE dir = ?", dir); err != nil {
		return err
	}

	// Insert symbols
	symStmt, err := tx.Prepare(`INSERT INTO call_graph_symbols 
		(dir, file, name, kind, signature, doc_comment, line, end_line, exported, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer symStmt.Close()

	for _, sym := range symbols {
		exported := 0
		if sym.Exported {
			exported = 1
		}
		if _, err := symStmt.Exec(dir, sym.File, sym.Name, string(sym.Kind), sym.Signature,
			sym.DocComment, sym.Line, sym.EndLine, exported, sym.ContentHash); err != nil {
			return err
		}
	}

	// Insert edges
	edgeStmt, err := tx.Prepare(`INSERT INTO call_graph_edges
		(dir, caller_name, callee_name, caller_file, callee_file, call_line, edge_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		if _, err := edgeStmt.Exec(dir, e.CallerName, e.CalleeName, e.CallerFile,
			e.CalleeFile, e.CallLine, e.EdgeKind); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LoadGraph reads the persisted graph for a directory.
func (s *CallGraphStore) LoadGraph(dir string) ([]CallGraphSymbol, []CallEdge, error) {
	// Load symbols
	rows, err := s.db.Query(`SELECT file, name, kind, signature, doc_comment, line, end_line, exported, content_hash
		FROM call_graph_symbols WHERE dir = ?`, dir)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var symbols []CallGraphSymbol
	for rows.Next() {
		var sym CallGraphSymbol
		var kindStr string
		var exported int
		if err := rows.Scan(&sym.File, &sym.Name, &kindStr, &sym.Signature, &sym.DocComment,
			&sym.Line, &sym.EndLine, &exported, &sym.ContentHash); err != nil {
			return nil, nil, err
		}
		sym.Kind = SymbolKind(kindStr)
		sym.Exported = exported != 0
		symbols = append(symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// Load edges
	edgeRows, err := s.db.Query(`SELECT caller_name, callee_name, caller_file, callee_file, call_line, edge_kind
		FROM call_graph_edges WHERE dir = ?`, dir)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()

	var edges []CallEdge
	for edgeRows.Next() {
		var e CallEdge
		if err := edgeRows.Scan(&e.CallerName, &e.CalleeName, &e.CallerFile, &e.CalleeFile,
			&e.CallLine, &e.EdgeKind); err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}

	return symbols, edges, nil
}

// IsStale returns files whose content hash no longer matches the stored hash.
func (s *CallGraphStore) IsStale(dir string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT file, content_hash FROM call_graph_symbols WHERE dir = ?`, dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type fileHash struct {
		file string
		hash string
	}
	var stored []fileHash
	for rows.Next() {
		var fh fileHash
		if err := rows.Scan(&fh.file, &fh.hash); err != nil {
			return nil, err
		}
		stored = append(stored, fh)
	}

	var stale []string
	for _, fh := range stored {
		fullPath := filepath.Join(dir, fh.file)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			stale = append(stale, fh.file) // file deleted or unreadable
			continue
		}
		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:])
		if hashStr != fh.hash {
			stale = append(stale, fh.file)
		}
	}

	return stale, nil
}
