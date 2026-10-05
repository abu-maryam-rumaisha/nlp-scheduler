package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/config"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/mq"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
)

type Config struct {
	AMQPURL        string
	Exchange       string
	Instance       string // AGENT_INSTANCE; names this agent's queue
	Service        string // AGENT_SERVICE
	Host           string
	HeartbeatEvery time.Duration
	Applier        *Applier
}

// ConfigFromEnv reads the AGENT_* settings (plus AMQP_URL / AMQP_EXCHANGE).
func ConfigFromEnv() (Config, error) {
	hostname, _ := os.Hostname()
	cfg := Config{
		AMQPURL:  config.String("AMQP_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange: config.String("AMQP_EXCHANGE", "openresty"),
		Instance: config.String("AGENT_INSTANCE", hostname),
		Service:  config.String("AGENT_SERVICE", ""),
		Host:     config.String("AGENT_HOST", hostname),
	}
	if !nginx.ResourceNameRe.MatchString(cfg.Instance) || !nginx.ResourceNameRe.MatchString(cfg.Service) {
		return cfg, fmt.Errorf("AGENT_INSTANCE (%q) and AGENT_SERVICE (%q) must match %s", cfg.Instance, cfg.Service, nginx.ResourceNameRe)
	}
	var err error
	if cfg.HeartbeatEvery, err = config.Duration("AGENT_HEARTBEAT_INTERVAL", 30*time.Second); err != nil {
		return cfg, err
	}
	cmdTimeout, err := config.Duration("AGENT_COMMAND_TIMEOUT", 30*time.Second)
	if err != nil {
		return cfg, err
	}
	cfg.Applier = &Applier{
		ConfDir:    config.String("AGENT_CONF_DIR", "/etc/nginx/conf.d"),
		TestCmd:    strings.Fields(config.String("AGENT_TEST_CMD", "openresty -t")),
		ReloadCmd:  strings.Fields(config.String("AGENT_RELOAD_CMD", "openresty -s reload")),
		CmdTimeout: cmdTimeout,
	}
	if st, err := os.Stat(cfg.Applier.ConfDir); err != nil || !st.IsDir() {
		return cfg, fmt.Errorf("AGENT_CONF_DIR %q is not a directory", cfg.Applier.ConfDir)
	}
	return cfg, nil
}

// Run consumes this instance's command queue until ctx is cancelled,
// reconnecting with backoff.
//
// Everything happens on the calling goroutine: commands are applied one at a
// time and heartbeats are sent between them, so two changes to the config
// directory or two reloads can never overlap. The broker delivers at most one
// unacknowledged command at a time (prefetch 1).
func Run(ctx context.Context, cfg Config) {
	slog.Info("agent starting", "instance", cfg.Instance, "service", cfg.Service, "conf_dir", cfg.Applier.ConfDir)
	backoff := time.Second
	for ctx.Err() == nil {
		err := session(ctx, cfg, func() { backoff = time.Second })
		if ctx.Err() != nil {
			break
		}
		slog.Warn("agent disconnected, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	slog.Info("agent stopped")
}

// session runs one connection's lifetime.
func session(ctx context.Context, cfg Config, connected func()) error {
	conn, err := amqp.Dial(cfg.AMQPURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	// One channel, in confirm mode, for both consuming and publishing.
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	if err := mq.DeclareExchange(ch, cfg.Exchange); err != nil {
		return err
	}
	queue, err := mq.DeclareAgentQueue(ch, cfg.Exchange, cfg.Service, cfg.Instance)
	if err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	connected()
	slog.Info("agent consuming", "queue", queue)

	ticker := time.NewTicker(cfg.HeartbeatEvery)
	defer ticker.Stop()
	heartbeat(ctx, ch, cfg)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-closed:
			return err
		case <-ticker.C:
			heartbeat(ctx, ch, cfg)
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			if err := deliver(ctx, ch, cfg, d); err != nil {
				return err
			}
		}
	}
}

// deliver applies one command and reports the result. If the result cannot
// be published, the command is requeued and the connection reset; applying
// it again is safe because apply is idempotent.
func deliver(ctx context.Context, ch *amqp.Channel, cfg Config, d amqp.Delivery) error {
	var cmd mq.Command
	if err := json.Unmarshal(d.Body, &cmd); err != nil {
		slog.Error("dropping malformed command", "err", err)
		return d.Ack(false)
	}
	// Do not abandon a half-applied change on shutdown; the applier's
	// command timeout still bounds it.
	res := handle(context.WithoutCancel(ctx), cfg, cmd)
	slog.Info("command handled", "deployment", cmd.DeploymentID, "action", cmd.Action,
		"config", cmd.ConfigName, "success", res.Success, "error", res.Error)

	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := mq.PublishJSON(pubCtx, ch, cfg.Exchange, mq.ResultKey(cfg.Service, cfg.Instance), res); err != nil {
		_ = d.Nack(false, true)
		return fmt.Errorf("publish result: %w", err)
	}
	return d.Ack(false)
}

func handle(ctx context.Context, cfg Config, cmd mq.Command) mq.Result {
	res := mq.Result{DeploymentID: cmd.DeploymentID, Service: cfg.Service, Instance: cfg.Instance, Success: true}
	var err error
	if cmd.Instance != cfg.Instance {
		err = fmt.Errorf("command addressed to %q delivered to %q", cmd.Instance, cfg.Instance)
	} else {
		err = cfg.Applier.Apply(ctx, cmd)
	}
	if err != nil {
		res.Success, res.Error = false, err.Error()
	}
	res.FinishedAt = time.Now().UTC()
	return res
}

func heartbeat(ctx context.Context, ch *amqp.Channel, cfg Config) {
	hb := mq.Heartbeat{Service: cfg.Service, Instance: cfg.Instance, Host: cfg.Host, At: time.Now().UTC()}
	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := mq.PublishJSON(pubCtx, ch, cfg.Exchange, mq.HeartbeatKey(cfg.Service, cfg.Instance), hb); err != nil && ctx.Err() == nil {
		slog.Warn("heartbeat failed", "err", err)
	}
}
