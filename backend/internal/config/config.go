// Package config reads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is shared by every role; each role only reads what it needs.
type Config struct {
	DatabaseURL string
	HTTPAddr    string
	Roles       map[string]bool // api, workers, dispatch, poller, scheduler
	InstanceID  string

	SampleDir string // sample data bundled into the image (for UI-driven injection)

	MockAPIURL   string        // upstream order API (poller)
	PollInterval time.Duration // normal poll cadence
	RobotURL     string        // robot dispatch endpoint

	WorkerConcurrency int // inbox goroutines per worker process
	MaxAttempts       int // inbox/outbox attempts before dead-letter

	SlotTZ      *time.Location
	MealTimes   map[string]string // meal -> "HH:MM"
	LeadMinutes int               // dispatch this long before a meal slot

	MaxCSVBytes int64
}

// Load builds a Config from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:       env("DATABASE_URL", "postgres://orders:orders@localhost:5432/orders?sslmode=disable"),
		HTTPAddr:          env("HTTP_ADDR", ":8080"),
		Roles:             parseRoles(env("ROLES", "api,workers,dispatch,poller,scheduler")),
		SampleDir:         env("SAMPLE_DIR", "../sample-data"),
		MockAPIURL:        env("MOCK_API_URL", "http://localhost:8090"),
		RobotURL:          env("ROBOT_URL", "http://localhost:8090/robot/dispatch"),
		WorkerConcurrency: envInt("WORKER_CONCURRENCY", 4),
		MaxAttempts:       envInt("MAX_ATTEMPTS", 6),
		LeadMinutes:       envInt("LEAD_MINUTES", 30),
		MaxCSVBytes:       int64(envInt("MAX_CSV_BYTES", 5<<20)),
	}
	host, _ := os.Hostname()
	c.InstanceID = env("INSTANCE_ID", fmt.Sprintf("%s-%d", host, os.Getpid()))

	d, err := time.ParseDuration(env("POLL_INTERVAL", "5s"))
	if err != nil {
		return c, fmt.Errorf("POLL_INTERVAL: %w", err)
	}
	c.PollInterval = d

	loc, err := time.LoadLocation(env("SLOT_TZ", "America/New_York"))
	if err != nil {
		return c, fmt.Errorf("SLOT_TZ: %w", err)
	}
	c.SlotTZ = loc

	c.MealTimes = map[string]string{}
	for _, kv := range strings.Split(env("MEAL_TIMES", "breakfast=08:00,lunch=12:00,dinner=18:00"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return c, fmt.Errorf("MEAL_TIMES: bad entry %q", kv)
		}
		if _, err := time.Parse("15:04", v); err != nil {
			return c, fmt.Errorf("MEAL_TIMES: bad time %q", v)
		}
		c.MealTimes[strings.ToLower(k)] = v
	}
	return c, nil
}

// Has reports whether this process runs the given role.
func (c Config) Has(role string) bool { return c.Roles[role] }

func parseRoles(s string) map[string]bool {
	m := map[string]bool{}
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			m[r] = true
		}
	}
	return m
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
