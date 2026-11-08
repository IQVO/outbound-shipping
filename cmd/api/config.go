package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// config is the api binary's environment.
type config struct {
	DatabaseURL string
	Brokers     []string
	Groups      map[string]string
	Addr        string
	LogLevel    string

	OutboxInterval   time.Duration
	OutboxBatch      int
	OutboxMaxAttempt int
}

// groupVars are the required consumer-group variables; the composition root
// reads them from the environment, never a literal (the architecture sensor
// pins this).
var groupVars = []string{
	"FULFILLMENT_CONSUMER_GROUP",
	"DOCK_CONSUMER_GROUP",
	"YARD_CONSUMER_GROUP",
	"CPT_CONSUMER_GROUP",
	"LAYOUT_CONSUMER_GROUP",
	"ANALYTICS_CONSUMER_GROUP",
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	intOr := func(key string, fallback int) int {
		if v := getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
		return fallback
	}

	var problems []error
	c := config{
		DatabaseURL:      getenv("DATABASE_URL"),
		Addr:             or("ADDR", ":8080"),
		LogLevel:         or("LOG_LEVEL", "info"),
		OutboxInterval:   time.Duration(intOr("OUTBOX_INTERVAL_MS", 250)) * time.Millisecond,
		OutboxBatch:      intOr("OUTBOX_BATCH", 100),
		OutboxMaxAttempt: intOr("OUTBOX_MAX_ATTEMPTS", 10),
	}
	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the service persists to Postgres only (no in-memory mode)"))
	}
	for _, b := range strings.Split(getenv("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Brokers = append(c.Brokers, b)
		}
	}
	if len(c.Brokers) == 0 {
		problems = append(problems, errors.New("KAFKA_BROKERS is required: the service consumes and publishes through Kafka"))
	}
	c.Groups = map[string]string{}
	for _, key := range groupVars {
		v := getenv(key)
		if v == "" {
			problems = append(problems, errors.New(key+" is required: consumer groups come from the environment, never a literal"))
			continue
		}
		c.Groups[key] = v
	}
	if len(problems) > 0 {
		return config{}, errors.Join(problems...)
	}
	return c, nil
}

// kafkaCheck is the /readyz dependency for the brokers: a plain TCP dial
// against the first broker, bounded by the readiness timeout.
func kafkaCheck(brokers []string) func(context.Context) error {
	return func(ctx context.Context) error {
		d := net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", brokers[0])
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
