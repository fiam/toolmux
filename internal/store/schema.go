package store

import "database/sql"

// schemaSQL creates every table up front. tool_calls is populated today;
// workflow_runs and workflow_steps are created now so workflow execution
// history can be recorded later without a migration, with tool_calls already
// carrying workflow_run_id / workflow_step columns to link a call to a step.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS tool_calls (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              TEXT NOT NULL,
    source          TEXT NOT NULL,
    cwd             TEXT,
    profile         TEXT,
    server_name     TEXT,
    tool_id         TEXT NOT NULL,
    provider        TEXT,
    resource        TEXT,
    action          TEXT,
    effect          TEXT,
    allowed         INTEGER NOT NULL,
    rule            TEXT,
    reason          TEXT,
    arguments       TEXT,
    error           TEXT,
    duration_ms     INTEGER,
    workflow_run_id INTEGER,
    workflow_step   TEXT
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_ts ON tool_calls(ts);
CREATE INDEX IF NOT EXISTS idx_tool_calls_cwd ON tool_calls(cwd);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool_id ON tool_calls(tool_id);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TEXT NOT NULL,
    cwd         TEXT,
    profile     TEXT,
    workflow    TEXT NOT NULL,
    status      TEXT,
    inputs      TEXT,
    finished_at TEXT,
    error       TEXT
);

CREATE TABLE IF NOT EXISTS workflow_steps (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    name        TEXT,
    input       TEXT,
    output      TEXT,
    status      TEXT,
    error       TEXT,
    ts          TEXT,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_workflow_steps_run ON workflow_steps(run_id);
`

func migrate(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	return err
}
