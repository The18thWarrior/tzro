package memory

// symbol_index.go — Symbol Index persistence for AST-extracted symbols.
//
// The Symbol Index is a side-channel table that accumulates {name, kind,
// signature, file, line} tuples emitted by the Symbol Extractor during
// Probe Node Thought Chain execution. See ADR-0047.

import (
	"fmt"
	"time"
	"tzro/internal/symbols"

	"github.com/google/uuid"
)

// InsertSymbol persists a single Symbol into the symbol_index table.
func (sdb *SqliteDatabase) InsertSymbol(probeID, taskID string, sym symbols.Symbol) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	exported := 0
	if sym.Exported {
		exported = 1
	}

	_, err := sdb.db.Exec(
		`INSERT OR IGNORE INTO symbol_index (id, probe_id, task_id, name, kind, signature, file, line, exported, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), probeID, taskID,
		sym.Name, string(sym.Kind), sym.Signature, sym.File, sym.Line, exported,
		time.Now().Unix(),
	)
	return err
}

// InsertSymbols persists a batch of Symbols into the symbol_index table.
func (sdb *SqliteDatabase) InsertSymbols(probeID, taskID string, syms []symbols.Symbol) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	if sdb.db == nil {
		return fmt.Errorf("database not initialized")
	}

	tx, err := sdb.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO symbol_index (id, probe_id, task_id, name, kind, signature, file, line, exported, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, sym := range syms {
		exported := 0
		if sym.Exported {
			exported = 1
		}
		if _, err := stmt.Exec(
			uuid.New().String(), probeID, taskID,
			sym.Name, string(sym.Kind), sym.Signature, sym.File, sym.Line, exported, now,
		); err != nil {
			return fmt.Errorf("failed to insert symbol %q: %w", sym.Name, err)
		}
	}

	return tx.Commit()
}

// GetSymbolIndex retrieves all symbols for a given probe, ordered by file and line.
func (sdb *SqliteDatabase) GetSymbolIndex(probeID string) ([]symbols.Symbol, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := sdb.db.Query(
		`SELECT name, kind, signature, file, line, exported
		FROM symbol_index WHERE probe_id = ? ORDER BY file ASC, line ASC`, probeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []symbols.Symbol
	for rows.Next() {
		var sym symbols.Symbol
		var kind string
		var exported int
		if err := rows.Scan(&sym.Name, &kind, &sym.Signature, &sym.File, &sym.Line, &exported); err != nil {
			return nil, fmt.Errorf("failed to scan symbol: %w", err)
		}
		sym.Kind = symbols.SymbolKind(kind)
		sym.Exported = exported == 1
		result = append(result, sym)
	}
	return result, nil
}

// GetSymbolIndexCount returns the number of symbols extracted for a given probe.
func (sdb *SqliteDatabase) GetSymbolIndexCount(probeID string) (int, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var count int
	err := sdb.db.QueryRow(
		`SELECT COUNT(*) FROM symbol_index WHERE probe_id = ?`, probeID,
	).Scan(&count)
	return count, err
}
