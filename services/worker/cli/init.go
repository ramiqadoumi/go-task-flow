package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultWorkerYAML = `# GoTaskFlow — Worker config
# Priority: CLI flag > this file > default.

kafka_brokers: "localhost:9092"
redis_addr:    "localhost:6379"
postgres_dsn:  "postgres://taskflow:taskflow@localhost:5432/taskflow?sslmode=disable"
log_level:     "info"

worker_type:  "email"   # email | webhook
max_retries:  3
task_timeout: "30s"     # accepts Go duration strings: 30s, 1m, 2m30s
metrics_addr: ":9091"   # :9092 for a second worker instance

# --- Local (MailHog) ---
smtp_host: "localhost"
smtp_port: 1025
smtp_from: "noreply@taskflow.dev"
# smtp_username: ""
# smtp_password: ""

# --- Gmail ---
# smtp_host:     "smtp.gmail.com"
# smtp_port:     587
# smtp_from:     "your@gmail.com"
# smtp_username: "your@gmail.com"
# smtp_password: "abcdefghijklmnop"  # Gmail App Password (not your account password)
# How to get an App Password:
#   1. Enable 2-Step Verification at myaccount.google.com/security
#   2. Go to App passwords → select Mail → Generate

# otel_endpoint: "localhost:4318"  # uncomment to enable OpenTelemetry tracing
`

func newInitCmd(serviceName, defaultYAML string) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a default config file",
		Long: fmt.Sprintf(`Write default configuration for %s.

If --config is given the file is written to that path.
Otherwise it is written to ~/.go-task-flow/%s.yaml.
Fails if the file already exists unless --force is passed.`, serviceName, serviceName),
		RunE: func(_ *cobra.Command, _ []string) error {
			dest := cfgFile
			if dest == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("home dir: %w", err)
				}
				dest = filepath.Join(home, ".go-task-flow", serviceName+".yaml")
			}

			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}

			if !force {
				if _, err := os.Stat(dest); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", dest)
				} else if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("stat %s: %w", dest, err)
				}
			}

			if err := os.WriteFile(dest, []byte(defaultYAML), 0o644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Printf("config written to %s\n", dest)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing config file")
	return cmd
}
