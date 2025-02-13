package main

import (
	"log/slog"
	"os"

	"github.com/pyama86/pachanger/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		slog.Error("failed to execute command", slog.Any("error", err))
		os.Exit(1)
	}
}
