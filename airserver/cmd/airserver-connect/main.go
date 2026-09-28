package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/michaelhsamy/home-assistant-airserver/airserver/internal/bridge"
)

func run() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	raw, err := os.ReadFile("/data/options.json")
	if err != nil {
		logger.Error("Could not read app configuration")
		return 1
	}
	options, err := bridge.ParseOptions(raw)
	if err != nil {
		logger.Error("Invalid app configuration", "error", err)
		return 1
	}
	registry, err := bridge.LoadRegistry("/data/registry.json")
	if err != nil {
		logger.Error("Could not load device registry", "error", err)
		return 1
	}
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		logger.Error("SUPERVISOR_TOKEN is missing; run this app through Home Assistant")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err = bridge.New(options, registry, logger).Run(ctx, token); err != nil {
		logger.Error("Bridge stopped", "error", err)
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
