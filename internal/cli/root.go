// Package cli wires all dandori subcommands (Cobra).
package cli

import (
	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

var (
	flagConfig string
	flagDB     string
)

var rootCmd = &cobra.Command{
	Use:          "dandori",
	Short:        "Dandori — outer harness console: capture, govern, learn your AI fleet",
	SilenceUsage: true,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "config file (default ~/.dandori/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&flagDB, "db", "", "sqlite db path (overrides config)")
}

// loadConfig resolves config honoring --config/--db flags.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return nil, err
	}
	if flagDB != "" {
		cfg.DBPath = flagDB
	}
	return cfg, nil
}

// openStore is the common config+store bootstrap for subcommands.
func openStore() (*config.Config, *store.Store, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	return cfg, st, nil
}
