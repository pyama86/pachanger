package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "0.0.20"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("preplacer version:", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
