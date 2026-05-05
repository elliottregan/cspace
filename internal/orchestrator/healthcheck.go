package orchestrator

import (
	"context"
	"fmt"
	"time"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

type execFn func(ctx context.Context, cmd []string) (stdout string, exitCode int, err error)

// waitHealthy polls the compose healthcheck until passing or until the
// retry budget is exhausted. Returns nil immediately when hc is nil
// (the service has no healthcheck declared).
func waitHealthy(ctx context.Context, hc *v2.Healthcheck, exec execFn) error {
	if hc == nil {
		return nil
	}
	cmd := normalizeHealthcheckCmd(hc.Test)
	if cmd == nil {
		return nil
	}
	if hc.StartPeriod > 0 {
		select {
		case <-time.After(hc.StartPeriod):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}
	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	interval := hc.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for i := 0; i < retries; i++ {
		hcCtx, cancel := context.WithTimeout(ctx, timeout)
		_, code, err := exec(hcCtx, cmd)
		cancel()
		if err == nil && code == 0 {
			return nil
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("healthcheck failed after %d retries", retries)
}

// normalizeHealthcheckCmd translates compose's healthcheck.test into a
// shell-runnable []string. Compose 'test' is one of:
//
//	["CMD", "exe", "arg", ...]      → ["exe", "arg", ...]
//	["CMD-SHELL", "shell-string"]  → ["sh", "-c", "shell-string"]
//	["NONE"]                       → nil (disable)
//	["plain string"]               → ["sh", "-c", "plain string"]
func normalizeHealthcheckCmd(test []string) []string {
	if len(test) == 0 || test[0] == "NONE" {
		return nil
	}
	if test[0] == "CMD" {
		return test[1:]
	}
	if test[0] == "CMD-SHELL" && len(test) >= 2 {
		return []string{"sh", "-c", test[1]}
	}
	return []string{"sh", "-c", test[0]}
}
