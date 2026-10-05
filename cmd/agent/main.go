// Command openresty-agent runs next to an OpenResty instance and applies the
// manager's up/down commands to its config directory. Run one per instance.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/agent"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/config"
)

func main() {
	if err := config.LoadEnvFile(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
	cfg, err := agent.ConfigFromEnv()
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agent.Run(ctx, cfg) // a single goroutine; returns when ctx is done
}
