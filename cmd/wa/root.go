// cmd/wa/root.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/config"
)

var (
	outputFormat string // "json" or "" (human-readable)
	configPath   string
	dbPath       string
	cfg          *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "wa",
	Short: "WhatsApp CLI & API tool",
	Long:  "wa is a command-line tool and REST API server for WhatsApp.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help
		if cmd.Name() == "help" {
			return nil
		}

		if configPath == "" {
			configPath = filepath.Join(config.Dir(), "config.yaml")
		}

		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Override DB path if flag set
		if dbPath != "" {
			cfg.Database.Path = dbPath
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "", "output format: json")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database file path")
}

// printOutput prints data as pretty-printed JSON. Individual commands provide
// their own human-readable formatting when --output is not "json".
func printOutput(data any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// exitError prints an error message and exits.
func exitError(msg string, code int) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(code)
}
