package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version string

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Run: func(cmd *cobra.Command, args []string) {
		if version == "" {
			version = "dev" // `-ldflags` が未設定なら "dev"
		}
		fmt.Println("preplacer version:", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
