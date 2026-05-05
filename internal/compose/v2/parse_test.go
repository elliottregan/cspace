package v2

import (
	"context"
	"testing"
	"time"
)

func TestParseMinimal(t *testing.T) {
	p, err := Parse(context.Background(), "testdata/minimal.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Services) != 1 {
		t.Fatalf("services count=%d", len(p.Services))
	}
	app := p.Services["app"]
	if app == nil {
		t.Fatal("app service missing")
	}
	if app.Image != "node:24-bookworm-slim" {
		t.Fatalf("image=%q", app.Image)
	}
	if app.Environment["FOO"] != "bar" {
		t.Fatalf("env FOO=%q", app.Environment["FOO"])
	}
}

func TestParseHealthcheckAndDependsOn(t *testing.T) {
	p, err := Parse(context.Background(), "testdata/with_healthcheck.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	be := p.Services["backend"]
	if be == nil || be.Healthcheck == nil {
		t.Fatal("backend or healthcheck nil")
	}
	if be.Healthcheck.Interval != 5*time.Second {
		t.Fatalf("interval=%v", be.Healthcheck.Interval)
	}
	if be.Healthcheck.StartPeriod != 10*time.Second {
		t.Fatalf("startPeriod=%v", be.Healthcheck.StartPeriod)
	}
	dash := p.Services["dashboard"]
	if dash == nil {
		t.Fatal("dashboard nil")
	}
	if len(dash.DependsOn) != 1 || dash.DependsOn[0].Name != "backend" || dash.DependsOn[0].Condition != "service_healthy" {
		t.Fatalf("dashboard depends_on=%+v", dash.DependsOn)
	}
}
