package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ramiqadoumi/go-task-flow/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("api-gateway %s\n", version.Version)
		fmt.Printf("  commit:     %s\n", version.GitCommit)
		fmt.Printf("  built:      %s\n", version.BuildTime)
		fmt.Printf("  go version: %s\n", version.GoVersion())
	},
}
