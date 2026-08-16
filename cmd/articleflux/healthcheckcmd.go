package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// `articleflux healthcheck` — the container's self-check.
//
// The Docker runtime image is distroless: no shell, no curl, nothing to ask
// /healthz with except this binary. So the binary asks itself, the same way
// AnimeFeedFlux and the portfolio server do it, and the exit code IS the
// answer — Docker's HEALTHCHECK reads nothing else. The endpoint probed is the
// exact /healthz deploy/articleflux-health.sh has always gated on, so the
// container's notion of healthy and the ops timer's cannot drift apart.
//
// Deliberately not a liveness ping of the process: a process that is up while
// /healthz answers 500 is DOWN for every purpose that matters, and reporting
// it healthy would let a health-gated deploy conclude a broken release worked.
func healthcheckCmd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := fs.String("url", "http://127.0.0.1:9000/healthz", "health endpoint to probe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s answered %s", *url, resp.Status)
	}
	return nil
}
