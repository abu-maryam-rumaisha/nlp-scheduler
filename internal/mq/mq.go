// Package mq defines the RabbitMQ topology and messages shared by the API
// and the agents.
//
// Everything goes through one durable topic exchange:
//
//	config.<service>.<instance>     API -> agent   up/down command
//	status.<service>.<instance>     agent -> API   result of a command
//	heartbeat.<service>.<instance>  agent -> API   liveness
//
// Each agent owns a durable queue (openresty.agent.<instance>) bound to its
// own command key, so commands wait in the queue while the agent is down.
// API replicas share one queue (openresty.api.events) bound to status.# and
// heartbeat.#, so each event is handled once.
package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	APIEventsQueue = "openresty.api.events"

	ActionUp   = "up"
	ActionDown = "down"
)

func CommandKey(service, instance string) string   { return "config." + service + "." + instance }
func ResultKey(service, instance string) string    { return "status." + service + "." + instance }
func HeartbeatKey(service, instance string) string { return "heartbeat." + service + "." + instance }
func AgentQueue(instance string) string            { return "openresty.agent." + instance }

// Command tells an agent to bring a config up (write <DeploymentID>.conf and
// remove the config's older files) or down (remove all of its files).
type Command struct {
	DeploymentID int64     `json:"deployment_id"`
	Action       string    `json:"action"`
	Service      string    `json:"service"`
	Instance     string    `json:"instance"`
	ConfigName   string    `json:"config_name"`
	Config       string    `json:"config,omitempty"`
	IssuedAt     time.Time `json:"issued_at"`
}

// Result reports the outcome of a Command.
type Result struct {
	DeploymentID int64     `json:"deployment_id"`
	Service      string    `json:"service"`
	Instance     string    `json:"instance"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
	FinishedAt   time.Time `json:"finished_at"`
}

type Heartbeat struct {
	Service  string    `json:"service"`
	Instance string    `json:"instance"`
	Host     string    `json:"host"`
	At       time.Time `json:"at"`
}

func DeclareExchange(ch *amqp.Channel, exchange string) error {
	return ch.ExchangeDeclare(exchange, amqp.ExchangeTopic, true, false, false, false, nil)
}

// DeclareAgentQueue declares an agent's queue and binds it to its command
// key. Both the agent and the API call it, so commands published before the
// agent's first start are kept.
func DeclareAgentQueue(ch *amqp.Channel, exchange, service, instance string) (string, error) {
	q, err := ch.QueueDeclare(AgentQueue(instance), true, false, false, false, nil)
	if err != nil {
		return "", err
	}
	return q.Name, ch.QueueBind(q.Name, CommandKey(service, instance), exchange, false, nil)
}

func DeclareAPIEventsQueue(ch *amqp.Channel, exchange string) (string, error) {
	q, err := ch.QueueDeclare(APIEventsQueue, true, false, false, false, nil)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"status.#", "heartbeat.#"} {
		if err := ch.QueueBind(q.Name, key, exchange, false, nil); err != nil {
			return "", err
		}
	}
	return q.Name, nil
}

// Publisher publishes persistent JSON messages and waits for broker
// confirmation. It redials lazily after the connection drops.
type Publisher struct {
	url      string
	exchange string

	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewPublisher(url, exchange string) *Publisher {
	return &Publisher{url: url, exchange: exchange}
}

func (p *Publisher) channel() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	if p.conn == nil || p.conn.IsClosed() {
		conn, err := amqp.Dial(p.url)
		if err != nil {
			return nil, fmt.Errorf("dial rabbitmq: %w", err)
		}
		p.conn = conn
	}
	ch, err := p.conn.Channel()
	if err != nil {
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		ch.Close()
		return nil, err
	}
	if err := DeclareExchange(ch, p.exchange); err != nil {
		ch.Close()
		return nil, err
	}
	p.ch = ch
	return ch, nil
}

// WithChannel runs fn on the publisher's channel, e.g. to declare queues.
func (p *Publisher) WithChannel(fn func(ch *amqp.Channel) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, err := p.channel()
	if err != nil {
		return err
	}
	return fn(ch)
}

func (p *Publisher) Publish(ctx context.Context, key string, msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, err := p.channel()
	if err != nil {
		return err
	}
	return PublishJSON(ctx, ch, p.exchange, key, msg)
}

// PublishJSON publishes a persistent JSON message on ch, which must be in
// confirm mode, and waits for the broker to confirm it.
func PublishJSON(ctx context.Context, ch *amqp.Channel, exchange, key string, msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conf, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, key, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("broker rejected message")
	}
	return nil
}

func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Close()
	}
}

// Consume runs until ctx is cancelled, reconnecting with backoff. setup
// declares topology and returns the queue to read. A handler error requeues
// the delivery after a short pause; handlers should ack (return nil)
// messages that can never succeed, such as malformed JSON.
func Consume(ctx context.Context, url string, prefetch int, setup func(*amqp.Channel) (string, error), handle func(context.Context, amqp.Delivery) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := consumeOnce(ctx, url, prefetch, setup, handle, func() { backoff = time.Second })
		if ctx.Err() != nil {
			return
		}
		slog.Warn("rabbitmq consumer disconnected, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func consumeOnce(ctx context.Context, url string, prefetch int, setup func(*amqp.Channel) (string, error), handle func(context.Context, amqp.Delivery) error, connected func()) error {
	conn, err := amqp.Dial(url)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return err
	}
	queue, err := setup(ch)
	if err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	connected()
	slog.Info("consuming", "queue", queue)

	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-closed:
			return err
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			if err := handle(ctx, d); err != nil {
				slog.Error("handling message failed, requeueing", "routing_key", d.RoutingKey, "err", err)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}
}
