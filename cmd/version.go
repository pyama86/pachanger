package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var version = "v0.0.1"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("preplacer version:", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
