// cmd/wa/version.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// commit and buildDate are stamped at release build time with
// -ldflags "-X main.commit=... -X main.buildDate=..."; a dev build prints
// the defaults below (issue #23).
var (
	commit    = "dev"
	buildDate = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the version of wa",
	Run: func(cmd *cobra.Command, args []string) {
		out := fmt.Sprintf("wa %s (commit %s", version, commit)
		if buildDate != "" {
			out += fmt.Sprintf(", built %s", buildDate)
		}
		fmt.Println(out + ")")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
