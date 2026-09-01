// cmd/wa/apikey.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/config"
)

// `wa apikey` manages the REST API key without hand-editing the config file
// (issue #23). Rotation writes through config.Save, which is the one
// sanctioned way this tool persists config.

var apikeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Manage the REST API key",
}

var apikeyShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the configured API key",
	Run: func(cmd *cobra.Command, args []string) {
		key := cfg.APIKey
		if key == "" {
			fmt.Println("No API key is configured; the API accepts requests without authentication.")
			return
		}
		fmt.Printf("API key: %s\n", key)
		fmt.Printf("Fingerprint: %s (safe to log)\n", config.KeyFingerprint(key))
		if os.Getenv(config.EnvAPIKey) != "" {
			fmt.Printf("Note: %s is set in the environment; it is what the running server actually uses.\n", config.EnvAPIKey)
		}
	},
}

var apikeyRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Generate a new API key and persist it",
	Run: func(cmd *cobra.Command, args []string) {
		newKey := config.GenerateAPIKey()
		if err := config.Save(configPath, withAPIKey(newKey)); err != nil {
			exitError(fmt.Sprintf("saving config: %v", err), 1)
		}
		fmt.Printf("Rotated API key: %s\n", newKey)
		fmt.Printf("Saved to %s\n", configPath)
		apikeyRotationNotice()
	},
}

var apikeySetCmd = &cobra.Command{
	Use:   "set <key>",
	Short: "Set a specific API key and persist it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		if len(key) < 16 {
			exitError("refusing to set a key shorter than 16 characters", 1)
		}
		if err := config.Save(configPath, withAPIKey(key)); err != nil {
			exitError(fmt.Sprintf("saving config: %v", err), 1)
		}
		fmt.Printf("API key set (fingerprint %s), saved to %s\n", config.KeyFingerprint(key), configPath)
		apikeyRotationNotice()
	},
}

// withAPIKey returns a copy of the loaded config with the key replaced. The
// in-memory cfg is rebuilt by cobra's PersistentPreRunE, so mutating it would
// be lost and surprising; Save only ever sees an explicit value.
func withAPIKey(key string) *config.Config {
	c := *cfg
	c.APIKey = key
	return &c
}

// apikeyRotationNotice explains what a persisted key change does and does not
// affect: a live server keeps serving on the key it started with.
func apikeyRotationNotice() {
	if os.Getenv(config.EnvAPIKey) != "" {
		fmt.Printf("Note: %s is set in the environment and overrides the saved key.\n", config.EnvAPIKey)
	}
	fmt.Println("A running server keeps its old key until restarted. Webhook HMAC secrets are unaffected.")
}

func init() {
	apikeyCmd.AddCommand(apikeyShowCmd, apikeyRotateCmd, apikeySetCmd)
	rootCmd.AddCommand(apikeyCmd)
}
