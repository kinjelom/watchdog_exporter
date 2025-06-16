package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"watchdog_exporter/config"
)

func makeBasicConfig() *config.WatchDogConfig {
	return &config.WatchDogConfig{
		Metrics: config.MetricsContext{
			Namespace:   "ns",
			Environment: "env",
		},
		Settings: config.ProgramSettings{
			ResponseBodyLimit: 1024,
			Debug:             false,
			MaxWorkersCount:   1,
			DefaultTimeout:    500 * time.Millisecond,
		},
		Endpoints: map[string]config.Endpoint{},
		Routes:    map[string]config.Route{},
	}
}

func TestBuildInfoMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWatchDogMetrics("myprog", "v1.2.3", cfg)
	// unregister from default registry after test to avoid duplicate registration in other tests
	defer func() {
		prometheus.Unregister(m.BuildInfo)
		prometheus.Unregister(m.EndpointValidation)
		prometheus.Unregister(m.EndpointDuration)
	}()

	// set the gauge value
	m.BuildInfo.With(nil).Set(1)

	// verify value via testutil
	if got := testutil.ToFloat64(m.BuildInfo.With(nil)); got != 1 {
		t.Fatalf("expected BuildInfo 1, got %v", got)
	}
}

func TestEndpointValidationMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWatchDogMetrics("prog", "ver", cfg)
	// unregister from default registry after test to avoid duplicate registration in other tests
	defer func() {
		prometheus.Unregister(m.BuildInfo)
		prometheus.Unregister(m.EndpointValidation)
		prometheus.Unregister(m.EndpointDuration)
	}()

	labels := prometheus.Labels{
		"endpoint": "ep1",
		"protocol": "http",
		"url":      "http://example.com",
		"route":    "r1",
		"valid":    "true",
	}
	// set gauge
	m.EndpointValidation.With(labels).Set(1)

	// verify
	if got := testutil.ToFloat64(m.EndpointValidation.With(labels)); got != 1 {
		t.Fatalf("expected EndpointValidation 1 for labels %v, got %v", labels, got)
	}
}

func TestEndpointDurationMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWatchDogMetrics("prog", "ver", cfg)
	// unregister from default registry after test to avoid duplicate registration in other tests
	defer func() {
		prometheus.Unregister(m.BuildInfo)
		prometheus.Unregister(m.EndpointValidation)
		prometheus.Unregister(m.EndpointDuration)
	}()

	labels := prometheus.Labels{
		"endpoint": "epX",
		"protocol": "https",
		"url":      "https://foo.bar",
		"route":    "routeA",
		"valid":    "false",
	}
	// set gauge
	m.EndpointDuration.With(labels).Set(1.234)

	// verify
	if got := testutil.ToFloat64(m.EndpointDuration.With(labels)); got != 1.234 {
		t.Fatalf("expected EndpointDuration 1.234 for labels %v, got %v", labels, got)
	}
}
