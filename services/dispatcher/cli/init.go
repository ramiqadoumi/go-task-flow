package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultDispatcherYAML = `# GoTaskFlow — Dispatcher config
# Priority: CLI flag > this file > default.

kafka_brokers: "localhost:9092"
redis_addr:    "localhost:6379"
log_level:     "info"
rate_limit:    100          # max tasks/second per task type (0 = disabled)
metrics_addr:  ":9094"

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
