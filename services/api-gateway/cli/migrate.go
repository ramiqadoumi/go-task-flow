package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ramiqadoumi/go-task-flow/internal/postgres/migrations"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	Long: `Connect to PostgreSQL and apply schema migrations.

Reads the DSN from --postgres-dsn flag, POSTGRES_DSN env var, or config file.`,
	RunE: runMigrate,
}


func runMigrate(_ *cobra.Command, _ []string) error {
	dsn := viper.GetString("postgres_dsn")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	files := []string{
		"001_create_tasks.sql",
		"002_create_executions.sql",
	}

	for _, f := range files {
		sql, err := migrations.FS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("execute migration %s: %w", f, err)
		}
		fmt.Printf("applied %s\n", f)
	}

	fmt.Println("migrations complete")
	return nil
}
