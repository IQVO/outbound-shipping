package main

import "testing"

// requiredEnv is the minimum environment loadConfig accepts.
func requiredEnv(extra map[string]string) func(string) string {
	env := map[string]string{
		"DATABASE_URL":               "postgres://x",
		"KAFKA_BROKERS":              "localhost:9092",
		"FULFILLMENT_CONSUMER_GROUP": "g1",
		"DOCK_CONSUMER_GROUP":        "g2",
		"YARD_CONSUMER_GROUP":        "g3",
		"CPT_CONSUMER_GROUP":         "g4",
		"LAYOUT_CONSUMER_GROUP":      "g5",
		"ANALYTICS_CONSUMER_GROUP":   "g6",
	}
	for k, v := range extra {
		env[k] = v
	}
	return func(k string) string { return env[k] }
}

// TestLoadConfigOutboxMaxAttempts pins the fleet norm: default 20, an explicit
// 0 means unlimited, negative and non-numeric values fall back to the default.
func TestLoadConfigOutboxMaxAttempts(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		want  int
	}{
		{"unset", nil, 20},
		{"empty", map[string]string{"OUTBOX_MAX_ATTEMPTS": ""}, 20},
		{"explicit 20", map[string]string{"OUTBOX_MAX_ATTEMPTS": "20"}, 20},
		{"explicit 5", map[string]string{"OUTBOX_MAX_ATTEMPTS": "5"}, 5},
		{"zero is unlimited", map[string]string{"OUTBOX_MAX_ATTEMPTS": "0"}, 0},
		{"negative falls back", map[string]string{"OUTBOX_MAX_ATTEMPTS": "-1"}, 20},
		{"garbage falls back", map[string]string{"OUTBOX_MAX_ATTEMPTS": "abc"}, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfig(requiredEnv(tc.extra))
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.OutboxMaxAttempt != tc.want {
				t.Fatalf("OutboxMaxAttempt = %d, want %d", cfg.OutboxMaxAttempt, tc.want)
			}
		})
	}
}

// TestLoadConfigOtherIntsStayPositiveOnly: the interval and batch knobs keep
// the positive-only rule (0 is not a meaningful interval or batch).
func TestLoadConfigOtherIntsStayPositiveOnly(t *testing.T) {
	cfg, err := loadConfig(requiredEnv(map[string]string{"OUTBOX_BATCH": "0", "OUTBOX_INTERVAL_MS": "-3"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutboxBatch != 100 || cfg.OutboxInterval.Milliseconds() != 250 {
		t.Fatalf("batch=%d interval=%v, want 100 / 250ms", cfg.OutboxBatch, cfg.OutboxInterval)
	}
}
