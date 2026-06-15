package memory

import (
	"database/sql"
)

type DashboardSpec struct {
	ID              string `json:"id"`
	Spec            string `json:"spec"`
	GeneratedAt     int64  `json:"generatedAt"`
	GeneratorTaskID string `json:"generatorTaskId"`
	TTLSeconds      int64  `json:"ttlSeconds"`
}

// SaveDashboardSpec saves the new dashboard spec to SQLite and prunes historical entries keeping max 10
func (sdb *SqliteDatabase) SaveDashboardSpec(id string, spec string, generatedAt int64, generatorTaskID string, ttlSeconds int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert new spec
	_, err = tx.Exec(`INSERT INTO dashboard_specs (id, spec, generated_at, generator_task_id, ttl_seconds)
		VALUES (?, ?, ?, ?, ?)`, id, spec, generatedAt, generatorTaskID, ttlSeconds)
	if err != nil {
		return err
	}

	// Prune old specs (keep max 10)
	_, err = tx.Exec(`DELETE FROM dashboard_specs WHERE id NOT IN (
		SELECT id FROM dashboard_specs ORDER BY generated_at DESC LIMIT 10
	)`)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetLatestDashboardSpec fetches the latest compiled dashboard spec from SQLite
func (sdb *SqliteDatabase) GetLatestDashboardSpec() (*DashboardSpec, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return nil, nil
	}

	var ds DashboardSpec
	var generatorTaskID sql.NullString

	err := sdb.db.QueryRow(`SELECT id, spec, generated_at, generator_task_id, ttl_seconds 
		FROM dashboard_specs ORDER BY generated_at DESC LIMIT 1`).
		Scan(&ds.ID, &ds.Spec, &ds.GeneratedAt, &generatorTaskID, &ds.TTLSeconds)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if generatorTaskID.Valid {
		ds.GeneratorTaskID = generatorTaskID.String
	}
	return &ds, nil
}
