
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:          "worker",
	Short:        "GoTaskFlow Worker — executes tasks consumed from Kafka",
	SilenceUsage: true,
}

// Execute is the entry point called from cmd/worker/main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path (default: ./config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level: debug | info | warn | error")
	bindFlag("log_level", rootCmd.PersistentFlags(), "log-level")

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(newInitCmd("worker", defaultWorkerYAML))
	rootCmd.AddCommand(versionCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := os.UserHomeDir()
		viper.SetConfigName("worker")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath(home + "/.go-task-flow")
		viper.AddConfigPath("/etc/go-task-flow")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		_, notFound := err.(viper.ConfigFileNotFoundError)
		if !notFound && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "error reading config file:", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "config:", viper.ConfigFileUsed())
	}
}

func buildLogger(level, service string) *slog.Logger {
	lvl := slog.LevelInfo
	if level == "debug" {
		lvl = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).
		With(slog.String("service", service))
}

func bindFlag(viperKey string, fs *pflag.FlagSet, flagName string) {
	if err := viper.BindPFlag(viperKey, fs.Lookup(flagName)); err != nil {
		panic(fmt.Sprintf("bindFlag %q → %q: %v", flagName, viperKey, err))
	}
}
