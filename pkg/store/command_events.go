package store

import (
	"database/sql"
	"time"
)

// StoredCommandEvent mirrors the persisted command event in the Content-Hash Store.
type StoredCommandEvent struct {
	ID          string     `json:"id"`
	Workspace   string     `json:"workspace"`
	SessionID   string     `json:"session_id"`
	ShellID     string     `json:"shell_id"`
	DisplayText string     `json:"display_text"`
	Cwd         string     `json:"cwd"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExitStatus  *int       `json:"exit_status,omitempty"`
}

// RecordCommandStart records the start of a command execution.
func (s *Store) RecordCommandStart(id, workspace, sessionID, shellID, displayText, cwd string, startedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO command_events (id, workspace, session_id, shell_id, display_text, cwd, started_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		workspace=excluded.workspace,
		session_id=excluded.session_id,
		shell_id=excluded.shell_id,
		display_text=excluded.display_text,
		cwd=excluded.cwd,
		started_at=excluded.started_at;
	`
	_, err := s.db.Exec(query, id, workspace, sessionID, shellID, displayText, cwd, startedAt.UTC().UnixNano())
	return err
}

// RecordCommandComplete correlates and records the completion of a command execution with its observed exit status.
func (s *Store) RecordCommandComplete(id string, exitStatus int, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE command_events SET completed_at = ?, exit_status = ? WHERE id = ?`
	_, err := s.db.Exec(query, completedAt.UTC().UnixNano(), exitStatus, id)
	return err
}

// GetCommandEvents retrieves command events for a workspace and optional session ID, ordered by start time ascending.
func (s *Store) GetCommandEvents(workspace, sessionID string, limit int) ([]StoredCommandEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	var query string
	var args []any
	if sessionID != "" {
		query = `SELECT id, workspace, session_id, shell_id, display_text, cwd, started_at, completed_at, exit_status
			FROM command_events WHERE workspace = ? AND session_id = ? ORDER BY started_at ASC LIMIT ?`
		args = []any{workspace, sessionID, limit}
	} else {
		query = `SELECT id, workspace, session_id, shell_id, display_text, cwd, started_at, completed_at, exit_status
			FROM command_events WHERE workspace = ? ORDER BY started_at ASC LIMIT ?`
		args = []any{workspace, limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []StoredCommandEvent
	for rows.Next() {
		var e StoredCommandEvent
		var startedAtNano int64
		var completedAtNano sql.NullInt64
		var exitStatus sql.NullInt64

		if err := rows.Scan(&e.ID, &e.Workspace, &e.SessionID, &e.ShellID, &e.DisplayText, &e.Cwd, &startedAtNano, &completedAtNano, &exitStatus); err != nil {
			return nil, err
		}
		e.StartedAt = time.Unix(0, startedAtNano).UTC()
		if completedAtNano.Valid {
			t := time.Unix(0, completedAtNano.Int64).UTC()
			e.CompletedAt = &t
		}
		if exitStatus.Valid {
			status := int(exitStatus.Int64)
			e.ExitStatus = &status
		}
		events = append(events, e)
	}
	return events, nil
}

// ClearCommandEvents deletes command events and resets capture gaps for the given workspace (or all if workspace is empty).
func (s *Store) ClearCommandEvents(workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if workspace == "" {
		_, err := s.db.Exec(`DELETE FROM command_events`)
		if err != nil {
			return err
		}
		_, _ = s.db.Exec(`DELETE FROM command_capture_gaps`)
		return nil
	}

	_, err := s.db.Exec(`DELETE FROM command_events WHERE workspace = ?`, workspace)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`DELETE FROM command_capture_gaps WHERE workspace = ?`, workspace)
	return nil
}

// PruneCommandEvents enforces retention limits: deleting events older than maxAge and keeping at most maxCount.
func (s *Store) PruneCommandEvents(workspace string, maxCount int, maxAge time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if maxAge > 0 {
		cutoffNano := time.Now().UTC().Add(-maxAge).UnixNano()
		if workspace != "" {
			_, _ = s.db.Exec(`DELETE FROM command_events WHERE workspace = ? AND started_at < ?`, workspace, cutoffNano)
		} else {
			_, _ = s.db.Exec(`DELETE FROM command_events WHERE started_at < ?`, cutoffNano)
		}
	}

	if maxCount > 0 {
		if workspace != "" {
			query := `DELETE FROM command_events WHERE workspace = ? AND id NOT IN (
				SELECT id FROM command_events WHERE workspace = ? ORDER BY started_at DESC LIMIT ?
			)`
			_, _ = s.db.Exec(query, workspace, workspace, maxCount)
		} else {
			query := `DELETE FROM command_events WHERE id NOT IN (
				SELECT id FROM command_events ORDER BY started_at DESC LIMIT ?
			)`
			_, _ = s.db.Exec(query, maxCount)
		}
	}
	return nil
}

// RecordCaptureGap records a storage or capture failure gap for the workspace.
func (s *Store) RecordCaptureGap(workspace string, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if count <= 0 {
		count = 1
	}
	now := time.Now().UTC().UnixNano()
	query := `
	INSERT INTO command_capture_gaps (workspace, gap_count, last_gap_at)
	VALUES (?, ?, ?)
	ON CONFLICT(workspace) DO UPDATE SET
		gap_count = command_capture_gaps.gap_count + excluded.gap_count,
		last_gap_at = excluded.last_gap_at;
	`
	_, err := s.db.Exec(query, workspace, count, now)
	return err
}

// GetCaptureGapCount retrieves the recorded capture gap count for the workspace.
func (s *Store) GetCaptureGapCount(workspace string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT gap_count FROM command_capture_gaps WHERE workspace = ?`, workspace).Scan(&count)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}
