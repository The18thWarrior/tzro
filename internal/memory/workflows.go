package memory

import (
	"database/sql"
	"fmt"
	"time"
)

func (sdb *SqliteDatabase) SaveWorkflow(wf WorkflowDefinition, tasks []WorkflowTask) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Save or replace workflow definition
	_, err = tx.Exec(`INSERT OR REPLACE INTO workflows (id, name, description, trigger_type, trigger_config, status, next_run_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, wf.ID, wf.Name, wf.Description, wf.TriggerType, wf.TriggerConfig, wf.Status, wf.NextRunAt, wf.CreatedAt, wf.UpdatedAt)
	if err != nil {
		return err
	}

	// 2. Clear old tasks for this workflow
	_, err = tx.Exec(`DELETE FROM workflow_tasks WHERE workflow_id = ?`, wf.ID)
	if err != nil {
		return err
	}

	// 3. Save new tasks
	for _, t := range tasks {
		_, err = tx.Exec(`INSERT INTO workflow_tasks (workflow_id, task_template_id, name, instructions, dependencies)
			VALUES (?, ?, ?, ?, ?)`, wf.ID, t.TaskTemplateID, t.Name, t.Instructions, t.Dependencies)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) GetWorkflows() ([]WorkflowDefinition, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []WorkflowDefinition{}, nil
	}

	rows, err := sdb.db.Query("SELECT id, name, description, trigger_type, trigger_config, status, next_run_at, created_at, updated_at FROM workflows ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowDefinition
	for rows.Next() {
		var wf WorkflowDefinition
		var nextRun sql.NullInt64
		err := rows.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.TriggerType, &wf.TriggerConfig, &wf.Status, &nextRun, &wf.CreatedAt, &wf.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if nextRun.Valid {
			wf.NextRunAt = nextRun.Int64
		}
		list = append(list, wf)
	}
	if list == nil {
		list = []WorkflowDefinition{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) GetWorkflowTasks(wfID string) ([]WorkflowTask, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []WorkflowTask{}, nil
	}

	rows, err := sdb.db.Query("SELECT workflow_id, task_template_id, name, instructions, dependencies FROM workflow_tasks WHERE workflow_id = ?", wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowTask
	for rows.Next() {
		var t WorkflowTask
		var dep sql.NullString
		err := rows.Scan(&t.WorkflowID, &t.TaskTemplateID, &t.Name, &t.Instructions, &dep)
		if err != nil {
			return nil, err
		}
		if dep.Valid {
			t.Dependencies = dep.String
		}
		list = append(list, t)
	}
	if list == nil {
		list = []WorkflowTask{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) DeleteWorkflow(wfID string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("DELETE FROM workflows WHERE id = ?", wfID)
	return err
}

func (sdb *SqliteDatabase) ToggleWorkflow(wfID string, status string) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("UPDATE workflows SET status = ?, updated_at = ? WHERE id = ?", status, time.Now().Unix(), wfID)
	return err
}

func (sdb *SqliteDatabase) UpdateWorkflowNextRun(wfID string, nextRun int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	_, err := sdb.db.Exec("UPDATE workflows SET next_run_at = ? WHERE id = ?", nextRun, wfID)
	return err
}

func (sdb *SqliteDatabase) CreateWorkflowExecution(exec WorkflowExecution, taskRuns []WorkflowTaskExecution) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	tx, err := sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO workflow_executions (id, workflow_id, status, started_at)
		VALUES (?, ?, ?, ?)`, exec.ID, exec.WorkflowID, exec.Status, exec.StartedAt)
	if err != nil {
		return err
	}

	for _, tr := range taskRuns {
		var taskExecID sql.NullString
		if tr.TaskExecutionID != "" {
			taskExecID.String = tr.TaskExecutionID
			taskExecID.Valid = true
		}
		_, err = tx.Exec(`INSERT INTO workflow_task_executions (workflow_execution_id, task_template_id, task_execution_id, status, started_at)
			VALUES (?, ?, ?, ?, ?)`, tr.WorkflowExecutionID, tr.TaskTemplateID, taskExecID, tr.Status, tr.StartedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (sdb *SqliteDatabase) UpdateWorkflowExecutionStatus(execID string, status string, completedAt int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var completedVal interface{}
	if completedAt > 0 {
		completedVal = completedAt
	} else {
		completedVal = nil
	}

	_, err := sdb.db.Exec("UPDATE workflow_executions SET status = ?, completed_at = ? WHERE id = ?", status, completedVal, execID)
	return err
}

func (sdb *SqliteDatabase) UpdateWorkflowTaskExecution(execID string, taskTemplateID string, taskExecID string, status string, completedAt int64) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	var completedVal interface{}
	if completedAt > 0 {
		completedVal = completedAt
	} else {
		completedVal = nil
	}

	var taskExecVal interface{}
	if taskExecID != "" {
		taskExecVal = taskExecID
	} else {
		taskExecVal = nil
	}

	_, err := sdb.db.Exec(`UPDATE workflow_task_executions 
		SET status = ?, task_execution_id = ?, completed_at = ?
		WHERE workflow_execution_id = ? AND task_template_id = ?`, status, taskExecVal, completedVal, execID, taskTemplateID)
	return err
}

func (sdb *SqliteDatabase) GetWorkflowExecutions(wfID string) ([]WorkflowExecution, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var rows *sql.Rows
	var err error
	if wfID != "" {
		rows, err = sdb.db.Query("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions WHERE workflow_id = ? ORDER BY started_at DESC", wfID)
	} else {
		rows, err = sdb.db.Query("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions ORDER BY started_at DESC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []WorkflowExecution
	for rows.Next() {
		var exec WorkflowExecution
		var completed sql.NullInt64
		err := rows.Scan(&exec.ID, &exec.WorkflowID, &exec.Status, &exec.StartedAt, &completed)
		if err != nil {
			return nil, err
		}
		if completed.Valid {
			exec.CompletedAt = completed.Int64
		}
		list = append(list, exec)
	}
	if list == nil {
		list = []WorkflowExecution{}
	}
	return list, nil
}

func (sdb *SqliteDatabase) GetWorkflowExecutionDetails(execID string) (*WorkflowExecution, []WorkflowTaskExecution, error) {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	var exec WorkflowExecution
	var completed sql.NullInt64
	err := sdb.db.QueryRow("SELECT id, workflow_id, status, started_at, completed_at FROM workflow_executions WHERE id = ?", execID).
		Scan(&exec.ID, &exec.WorkflowID, &exec.Status, &exec.StartedAt, &completed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, fmt.Errorf("workflow execution '%s' not found", execID)
		}
		return nil, nil, err
	}
	if completed.Valid {
		exec.CompletedAt = completed.Int64
	}

	rows, err := sdb.db.Query("SELECT workflow_execution_id, task_template_id, task_execution_id, status, started_at, completed_at FROM workflow_task_executions WHERE workflow_execution_id = ?", execID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var taskRuns []WorkflowTaskExecution
	for rows.Next() {
		var tr WorkflowTaskExecution
		var taskExecID sql.NullString
		var completedVal sql.NullInt64
		err := rows.Scan(&tr.WorkflowExecutionID, &tr.TaskTemplateID, &taskExecID, &tr.Status, &tr.StartedAt, &completedVal)
		if err != nil {
			return nil, nil, err
		}
		if taskExecID.Valid {
			tr.TaskExecutionID = taskExecID.String
		}
		if completedVal.Valid {
			tr.CompletedAt = completedVal.Int64
		}
		taskRuns = append(taskRuns, tr)
	}
	if taskRuns == nil {
		taskRuns = []WorkflowTaskExecution{}
	}

	return &exec, taskRuns, nil
}
