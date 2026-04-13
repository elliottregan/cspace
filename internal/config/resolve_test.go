package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTemplate_ProjectOverride(t *testing.T) {
	dir := t.TempDir()

	// Create project override
	overridePath := filepath.Join(dir, ".cspace", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create assets fallback
	assetsDir := filepath.Join(dir, "assets")
	assetsPath := filepath.Join(assetsDir, "templates", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(assetsPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetsPath, []byte("FROM node"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ResolveTemplate(dir, assetsDir, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveTemplate() error: %v", err)
	}

	if result != overridePath {
		t.Errorf("expected project override %s, got %s", overridePath, result)
	}
}

func TestResolveTemplate_FallbackToAssets(t *testing.T) {
	dir := t.TempDir()

	// Only create assets fallback (no project override)
	assetsDir := filepath.Join(dir, "assets")
	assetsPath := filepath.Join(assetsDir, "templates", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(assetsPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetsPath, []byte("FROM node"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ResolveTemplate(dir, assetsDir, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveTemplate() error: %v", err)
	}

	if result != assetsPath {
		t.Errorf("expected assets fallback %s, got %s", assetsPath, result)
	}
}

func TestResolveTemplate_NotFound(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")

	_, err := ResolveTemplate(dir, assetsDir, "nonexistent.yml")
	if err == nil {
		t.Error("expected error for missing template")
	}
}

func TestResolveScript_ProjectOverride(t *testing.T) {
	dir := t.TempDir()

	// Create project override
	overridePath := filepath.Join(dir, ".cspace", "scripts", "entrypoint.sh")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte("#!/bin/bash"), 0755); err != nil {
		t.Fatal(err)
	}

	assetsDir := filepath.Join(dir, "assets")

	result := ResolveScript(dir, assetsDir, "entrypoint.sh")
	if result != overridePath {
		t.Errorf("expected project override %s, got %s", overridePath, result)
	}
}

func TestResolveScript_FallbackToAssets(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")

	// No project override — should fall back to assets path
	result := ResolveScript(dir, assetsDir, "entrypoint.sh")
	expected := filepath.Join(assetsDir, "scripts", "entrypoint.sh")
	if result != expected {
		t.Errorf("expected assets fallback %s, got %s", expected, result)
	}
}

func TestResolveAgent_ProjectOverride(t *testing.T) {
	dir := t.TempDir()

	// Create project override
	overridePath := filepath.Join(dir, ".cspace", "agents", "implementer.md")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte("# Custom agent"), 0644); err != nil {
		t.Fatal(err)
	}

	assetsDir := filepath.Join(dir, "assets")

	result := ResolveAgent(dir, assetsDir, "implementer.md")
	if result != overridePath {
		t.Errorf("expected project override %s, got %s", overridePath, result)
	}
}

func TestResolveAgent_FallbackToAssets(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")

	result := ResolveAgent(dir, assetsDir, "coordinator.md")
	expected := filepath.Join(assetsDir, "agents", "coordinator.md")
	if result != expected {
		t.Errorf("expected assets fallback %s, got %s", expected, result)
	}
}
