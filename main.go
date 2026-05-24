package main

import (
	"log/slog"
	"os"
)

func main() {
	app, err := CreateNewApp()
	if err != nil {
		slog.Error("create app", "err", err)
		os.Exit(1)
	}
	if err := app.Run(); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}
