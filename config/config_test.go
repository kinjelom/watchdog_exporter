package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig_FileNotFound(t *testing.T) {
	cfg, err := LoadConfig("nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on error, got %v", cfg)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "invalid-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func(name string) {
		_ = os.Remove(name)
	}(tmpfile.Name())

	_, writeErr := tmpfile.WriteString("not valid yaml")
	if writeErr != nil {
		t.Fatalf("failed to write to temp file: %v", writeErr)
	}
	_ = tmpfile.Close()

	cfg, err := LoadConfig(tmpfile.Name())
	if err == nil {
		t.Fatal("expected YAML parse error, got nil")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on error, got %v", cfg)
	}
}

func TestLoadConfig_Success(t *testing.T) {
	content := `
settings:
  listen-address: ":8080"
  telemetry-path: "/metrics"
  max-workers-count: 3
  default-timeout: 5s
  response-body-limit: 1024
  debug: true
metrics:
  namespace: "testns"
  environment: "dev"
routes:
  r1:
    proxy-url: "http://proxy"
    target-ip: "1.2.3.4"
endpoints:
  ep1:
    group: group-1
    protocol: "http"
    routes: ["r1"]
    request:
      timeout: 2s
      method: "GET"
      headers:
        Header1: "value1"
      url: "http://example.com"
    validation:
      status-code: 200
      headers:
        Content-Type: "application/json"
      body-regex: ".*"
`
	tmpfile, err := os.CreateTemp("", "valid-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func(name string) {
		_ = os.Remove(name)
	}(tmpfile.Name())

	_, writeErr := tmpfile.WriteString(content)
	if writeErr != nil {
		t.Fatalf("failed to write to temp file: %v", writeErr)
	}
	_ = tmpfile.Close()

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Settings.ListenAddress != ":8080" {
		t.Errorf("expected ListenAddress ':8080', got '%s'", cfg.Settings.ListenAddress)
	}
	if cfg.Settings.MaxWorkersCount != 3 {
		t.Errorf("expected MaxWorkersCount 3, got %d", cfg.Settings.MaxWorkersCount)
	}
	if cfg.Settings.ResponseBodyLimit != 1024 {
		t.Errorf("expected ResponseBodyLimit 1024, got %d", cfg.Settings.ResponseBodyLimit)
	}
	if !cfg.Settings.Debug {
		t.Errorf("expected Debug true, got false")
	}
	if cfg.Metrics.Namespace != "testns" {
		t.Errorf("expected Metrics.Namespace 'testns', got '%s'", cfg.Metrics.Namespace)
	}
	route, ok := cfg.Routes["r1"]
	if !ok {
		t.Error("expected Routes['r1'] present")
	} else {
		if route.ProxyUrl != "http://proxy" {
			t.Errorf("expected ProxyUrl 'http://proxy', got '%s'", route.ProxyUrl)
		}
		if route.TargetIP != "1.2.3.4" {
			t.Errorf("expected TargetIP '1.2.3.4', got '%s'", route.TargetIP)
		}
	}
	ep, ok := cfg.Endpoints["ep1"]
	if !ok {
		t.Error("expected Endpoints['ep1'] present")
	} else {
		if ep.Group != "group-1" {
			t.Errorf("expected Group 'group-1', got '%s'", ep.Protocol)
		}
		if ep.Protocol != "http" {
			t.Errorf("expected Protocol 'http', got '%s'", ep.Protocol)
		}
		if len(ep.Routes) != 1 || ep.Routes[0] != "r1" {
			t.Errorf("expected Routes ['r1'], got %v", ep.Routes)
		}
		if ep.Request.Timeout != 2*time.Second {
			t.Errorf("expected Request.Timeout 2s, got %v", ep.Request.Timeout)
		}
		if ep.Request.Method != "GET" {
			t.Errorf("expected Request.Method 'GET', got '%s'", ep.Request.Method)
		}
		if ep.Request.Headers["Header1"] != "value1" {
			t.Errorf("expected Header1 'value1', got '%s'", ep.Request.Headers["Header1"])
		}
		if ep.Request.URL != "http://example.com" {
			t.Errorf("expected URL 'http://example.com', got '%s'", ep.Request.URL)
		}
		if ep.Validation.StatusCode != 200 {
			t.Errorf("expected Validation.StatusCode 200, got %d", ep.Validation.StatusCode)
		}
		if ep.Validation.Headers["Content-Type"] != "application/json" {
			t.Errorf("expected Validation.Headers['Content-Type'] 'application/json', got '%s'", ep.Validation.Headers["Content-Type"])
		}
		if ep.Validation.BodyRegex != ".*" {
			t.Errorf("expected Validation.BodyRegex '.*', got '%s'", ep.Validation.BodyRegex)
		}
	}
}
