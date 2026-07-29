package db

import (
	"fmt"
)

// MysqlDialect implements DialectAdapter for MySQL
type MysqlDialect struct{}

func (d *MysqlDialect) DriverName() string {
	return "mysql"
}

func (d *MysqlDialect) DataSourceName(cfg ConnectionConfig) string {
	if cfg.ConnectionURL != "" {
		return cfg.ConnectionURL
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.DatabaseName, cfg.ExtraOptions)
}

func (d *MysqlDialect) TableInfoQuery(tableName string) string {
	return fmt.Sprintf("DESCRIBE %s", tableName)
}

func (d *MysqlDialect) SchemaInitQueries() []string {
	// MySQL schemas
	return []string{
		`CREATE TABLE IF NOT EXISTS fact_memories (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255),
			type VARCHAR(255),
			content TEXT,
			context TEXT,
			confidence DOUBLE,
			source VARCHAR(255),
			created_at DATETIME,
			embedding TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS kg_nodes (
			id VARCHAR(255) PRIMARY KEY,
			node_type VARCHAR(255),
			name VARCHAR(255),
			metadata TEXT,
			source VARCHAR(255),
			weight DOUBLE,
			embedding TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS kg_edges (
			id VARCHAR(255) PRIMARY KEY,
			edge_type VARCHAR(255),
			source_id VARCHAR(255),
			target_id VARCHAR(255),
			metadata TEXT,
			weight DOUBLE
		);`,
		`CREATE TABLE IF NOT EXISTS node_states (
			task_id VARCHAR(255),
			node_id VARCHAR(255),
			status VARCHAR(255),
			output TEXT,
			raw_output TEXT,
			completed_at BIGINT,
			PRIMARY KEY (task_id, node_id)
		);`,
		`CREATE TABLE IF NOT EXISTS skills (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255),
			trigger_description TEXT,
			sop_content TEXT,
			created_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS entity_types (
			id VARCHAR(255) PRIMARY KEY,
			label VARCHAR(255),
			color VARCHAR(255),
			icon VARCHAR(255),
			built_in INT
		);`,
		`CREATE TABLE IF NOT EXISTS disk_cache (
			cache_id VARCHAR(255) PRIMARY KEY,
			raw_payload TEXT,
			envelope_json TEXT,
			created_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS workflows (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			trigger_type VARCHAR(255) NOT NULL,
			trigger_config TEXT,
			status VARCHAR(255) NOT NULL DEFAULT 'active',
			next_run_at BIGINT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_tasks (
			workflow_id VARCHAR(255) NOT NULL,
			task_template_id VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			instructions TEXT NOT NULL,
			dependencies TEXT,
			PRIMARY KEY (workflow_id, task_template_id)
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_executions (
			id VARCHAR(255) PRIMARY KEY,
			workflow_id VARCHAR(255) NOT NULL,
			status VARCHAR(255) NOT NULL,
			started_at BIGINT NOT NULL,
			completed_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS workflow_task_executions (
			workflow_execution_id VARCHAR(255) NOT NULL,
			task_template_id VARCHAR(255) NOT NULL,
			task_execution_id VARCHAR(255),
			status VARCHAR(255) NOT NULL,
			started_at BIGINT,
			completed_at BIGINT,
			PRIMARY KEY (workflow_execution_id, task_template_id)
		);`,
		`CREATE TABLE IF NOT EXISTS openapi_integrations (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			openapi_spec TEXT NOT NULL,
			auth_type VARCHAR(255) NOT NULL,
			auth_key VARCHAR(255),
			auth_value VARCHAR(255),
			created_at BIGINT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS durable_notifications (
			id VARCHAR(255) PRIMARY KEY,
			source VARCHAR(255) NOT NULL,
			type VARCHAR(255) NOT NULL,
			title VARCHAR(255) NOT NULL,
			message TEXT NOT NULL,
			task_id VARCHAR(255),
			workflow_id VARCHAR(255),
			target_id VARCHAR(255),
			status VARCHAR(255) NOT NULL,
			action_payload TEXT,
			created_at BIGINT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS call_graph_symbols (
			dir VARCHAR(255) NOT NULL,
			file VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			kind VARCHAR(255) NOT NULL,
			signature TEXT,
			doc_comment TEXT,
			line INTEGER,
			end_line INTEGER,
			exported INTEGER,
			content_hash VARCHAR(255),
			PRIMARY KEY (dir, file, name)
		);`,
		`CREATE TABLE IF NOT EXISTS call_graph_edges (
			dir VARCHAR(255) NOT NULL,
			caller_name VARCHAR(255) NOT NULL,
			callee_name VARCHAR(255) NOT NULL,
			caller_file VARCHAR(255) NOT NULL,
			callee_file VARCHAR(255) NOT NULL,
			call_line INTEGER,
			edge_kind VARCHAR(255)
		);`,
	}
}

func (d *MysqlDialect) UpsertNodeStateQuery() string {
	return `INSERT INTO node_states (task_id, node_id, status, output, completed_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE status = VALUES(status), output = VALUES(output), completed_at = VALUES(completed_at)`
}

func (d *MysqlDialect) UpsertNotificationQuery() string {
	return `INSERT INTO durable_notifications 
		(id, source, type, title, message, task_id, workflow_id, target_id, status, action_payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE source = VALUES(source), type = VALUES(type), title = VALUES(title), 
		    message = VALUES(message), task_id = VALUES(task_id), workflow_id = VALUES(workflow_id), 
		    target_id = VALUES(target_id), status = VALUES(status), action_payload = VALUES(action_payload), 
		    created_at = VALUES(created_at)`
}

func (d *MysqlDialect) InsertMemoryQuery() string {
	return `INSERT IGNORE INTO fact_memories (id, user_id, type, content, context, confidence, source, created_at, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
}
