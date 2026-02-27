package migrations

import "embed"

//go:embed 001_create_tasks.sql 002_create_executions.sql
var FS embed.FS
