// Command openresty-manager serves the management API and web UI. Run one
// manager; it drives any number of agents (cmd/agent) through RabbitMQ.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/api"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/auth"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/config"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/deploy"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/mq"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/store"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadEnvFile(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runAPI(ctx)
}

// runAPI serves the HTTP API and consumes agent events until ctx is done.
func runAPI(ctx context.Context) error {
	var (
		addr     = config.String("HTTP_ADDR", ":8080")
		dsn      = config.String("DATABASE_URL", "postgres://openresty:openresty@localhost:5432/openresty?sslmode=disable")
		amqpURL  = config.String("AMQP_URL", "amqp://guest:guest@localhost:5672/")
		exchange = config.String("AMQP_EXCHANGE", "openresty")
		redisURL = config.String("REDIS_URL", "redis://localhost:6379/0")
	)
	onlineWindow, err := config.Duration("INSTANCE_ONLINE_WINDOW", 90*time.Second)
	if err != nil {
		return err
	}
	sessionTTL, err := config.Duration("SESSION_TTL", 12*time.Hour)
	if err != nil {
		return err
	}
	// Postgres and Redis may be managed elsewhere and still be starting.
	startupTimeout, err := config.Duration("STARTUP_TIMEOUT", 60*time.Second)
	if err != nil {
		return err
	}

	var st *store.Store
	err = retry(ctx, "postgres", startupTimeout, func() (err error) {
		st, err = store.Open(ctx, dsn)
		return err
	})
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := bootstrapUser(ctx, st); err != nil {
		return err
	}

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()
	if err := retry(ctx, "redis", startupTimeout, func() error { return rdb.Ping(ctx).Err() }); err != nil {
		return err
	}

	pub := mq.NewPublisher(amqpURL, exchange)
	defer pub.Close()
	svc := deploy.NewService(st, pub, exchange)

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() {
		mq.Consume(ctx, amqpURL, 32, func(ch *amqp.Channel) (string, error) {
			if err := mq.DeclareExchange(ch, exchange); err != nil {
				return "", err
			}
			return mq.DeclareAPIEventsQueue(ch, exchange)
		}, svc.HandleEvent)
	})

	ui := web.UI()
	if ui == nil {
		slog.Warn("web UI not built; only the API is served (run `pnpm build` in web/)")
	}
	app := api.NewServer(st, svc, auth.NewSessions(rdb, sessionTTL), api.Config{
		OnlineWindow: onlineWindow,
	}).App(ui)

	errc := make(chan error, 1)
	go func() {
		errc <- app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	slog.Info("http listening", "addr", addr)

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = app.ShutdownWithContext(shutdownCtx)
	return err
}

// retry calls fn until it succeeds, ctx ends or timeout passes, backing off
// between attempts.
func retry(ctx context.Context, what string, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if time.Now().Add(backoff).After(deadline) {
			return fmt.Errorf("connect to %s: %w", what, err)
		}
		slog.Warn("waiting for "+what, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

// bootstrapUser creates the first prosecutor from BOOTSTRAP_USERNAME and
// BOOTSTRAP_PASSWORD when the users table is empty.
func bootstrapUser(ctx context.Context, st *store.Store) error {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	username, password := os.Getenv("BOOTSTRAP_USERNAME"), os.Getenv("BOOTSTRAP_PASSWORD")
	if username == "" || password == "" {
		slog.Warn("no users exist; set BOOTSTRAP_USERNAME and BOOTSTRAP_PASSWORD to create the first prosecutor")
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	u := store.User{Username: username, Role: auth.RoleProsecutor, PasswordHash: hash}
	if err := st.CreateUser(ctx, &u); err != nil && !errors.Is(err, store.ErrConflict) {
		return err
	}
	slog.Info("created bootstrap prosecutor", "username", username)
	return nil
}
