// Package healthcheck lets a scratch image probe itself: `<binary> healthcheck <url>`
// exits 0 if the URL answers 2xx. Used by docker compose healthchecks.
package healthcheck

import (
	"net/http"
	"os"
	"time"
)

// RunIfRequested handles the healthcheck subcommand and exits; otherwise it returns.
func RunIfRequested() {
	if len(os.Args) < 3 || os.Args[1] != "healthcheck" {
		return
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(os.Args[2])
	if err != nil {
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		os.Exit(1)
	}
	os.Exit(0)
}
