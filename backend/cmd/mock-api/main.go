// Command mock-api replays api_responses.jsonl as a polling API and acts as
// the robot's dispatch endpoint.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jj0629/lab37-homework/backend/internal/healthcheck"
	"github.com/jj0629/lab37-homework/backend/internal/mockapi"
)

func main() {
	healthcheck.RunIfRequested()
	addr := flag.String("addr", envOr("MOCK_ADDR", ":8090"), "listen address")
	file := flag.String("file", envOr("MOCK_API_FILE", "../sample-data/api_responses.jsonl"), "responses file")
	step := flag.Duration("step", durOr("MOCK_API_STEP", 3*time.Second), "time between successive responses becoming available")
	fail := flag.Float64("robot-fail-rate", 0.05, "fraction of robot requests that return 503")
	flag.Parse()
	if *step <= 0 {
		*step = time.Millisecond
	}

	s, err := mockapi.New(*file, *step, *fail)
	if err != nil {
		slog.Error("load responses", "err", err)
		os.Exit(1)
	}
	slog.Info("mock API listening", "addr", *addr, "step", step.String())
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("http", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durOr(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return d
	}
	return def
}
