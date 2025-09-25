package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"watchdog_exporter/config"
	"watchdog_exporter/prober"
	"watchdog_exporter/validator"
)

func TestBuildInfoMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("myprog", "v1.2.3", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	// set the gauge value (already set to 1 in constructor, ale ustawiamy jawnie)
	m.BuildInfo.With(nil).Set(1)

	if got := testutil.ToFloat64(m.BuildInfo.With(nil)); got != 1 {
		t.Fatalf("expected BuildInfo 1, got %v", got)
	}
}

func TestEndpointValidationMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("prog", "ver", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	labels := prometheus.Labels{
		"group":    "group-1",
		"endpoint": "ep-1",
		"protocol": "http",
		"url":      "https://example.com",
		"route":    "r1",
		"status":   "valid",
		"is_error": "false",
	}
	m.EndpointValidation.With(labels).Set(1)

	if got := testutil.ToFloat64(m.EndpointValidation.With(labels)); got != 1 {
		t.Fatalf("expected EndpointValidation 1 for labels %v, got %v", labels, got)
	}
}

func TestEndpointDurationMetric(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("prog", "ver", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	labels := prometheus.Labels{
		"group":    "group-1",
		"endpoint": "ep-1",
		"protocol": "http",
		"url":      "https://example.org",
		"route":    "routeA",
		"status":   "err",
		"is_error": "true",
	}
	m.EndpointDuration.With(labels).Set(1.234)

	if got := testutil.ToFloat64(m.EndpointDuration.With(labels)); got != 1.234 {
		t.Fatalf("expected EndpointDuration 1.234 for labels %v, got %v", labels, got)
	}
}

func TestOnResult_SetsLastProbeTimestamp(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("prog", "ver", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	// Fixed timestamp to avoid flakiness
	at := time.Unix(1700000000, 0)

	r := prober.Result{
		Group:    "g",
		Endpoint: "ep",
		Protocol: "https",
		URL:      "https://example.io",
		Route:    "r",
		Status:   "ok",
		Duration: 0.42,
		Err:      nil,
		At:       at,
		// TLS intentionally nil here
	}

	m.OnResult(r)

	lblBase := prometheus.Labels{
		"group":    "g",
		"endpoint": "ep",
		"protocol": "https",
		"url":      "https://example.io",
		"route":    "r",
	}

	got := testutil.ToFloat64(m.EndpointLastProbeTimestamp.With(lblBase))
	want := float64(at.Unix())
	if got != want {
		t.Fatalf("endpoint_last_probe_timestamp_seconds got %v, want %v", got, want)
	}
}

func TestOnResult_TLS_OK_UsesEndpointValidation(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("prog", "ver", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	r := prober.Result{
		Group:    "g",
		Endpoint: "ep",
		Protocol: "https",
		URL:      "https://example.io",
		Route:    "r",
		Status:   "ok",
		Duration: 0.11,
		Err:      nil,
		At:       time.Unix(1700000000, 0),
		TLS: &validator.CertsReport{
			HadTLS:     true,
			ChainValid: true,
		},
	}

	m.OnResult(r)

	lblAll := prometheus.Labels{
		"group":    "g",
		"endpoint": "ep",
		"protocol": "https",
		"url":      "https://example.io",
		"route":    "r",
		"status":   "ok",
		"is_error": "false",
	}

	if got := testutil.ToFloat64(m.EndpointValidation.With(lblAll)); got != 1 {
		t.Fatalf("endpoint_validation got %v, want 1", got)
	}
}

func TestOnResult_TLSCertDaysLeft_ForLeaf(t *testing.T) {
	cfg := makeBasicConfig()
	m := NewWDMetrics("prog", "ver", cfg, newFakeProvider())
	t.Cleanup(func() { unregisterMetrics(m) })

	r := prober.Result{
		Group:    "g",
		Endpoint: "ep",
		Protocol: "https",
		URL:      "https://example.io",
		Route:    "r",
		Status:   "ok",
		Duration: 0.33,
		Err:      nil,
		At:       time.Unix(1700000000, 0),
		TLS: &validator.CertsReport{
			HadTLS:     true,
			ChainValid: true,
			Certificates: []validator.CertInfo{
				{
					Position:   0,
					SerialHex:  "ABCDEF",
					CommonName: "example.io",
					IsCA:       false,
					IssuerCN:   "R12",
					// NotAfter not needed in this metric
					DaysLeft: 10.0,
				},
			},
		},
	}

	m.OnResult(r)

	lblCert := prometheus.Labels{
		"group":          "g",
		"endpoint":       "ep",
		"protocol":       "https",
		"url":            "https://example.io",
		"route":          "r",
		"cert_position":  "0",
		"cert_serial":    "ABCDEF",
		"cert_cn":        "example.io",
		"cert_is_ca":     "false",
		"cert_issuer_cn": "R12",
	}

	if got := testutil.ToFloat64(m.EndpointTLSCertDaysLeft.With(lblCert)); got != 10.0 {
		t.Fatalf("endpoint_tls_cert_days_left got %v, want 10.0", got)
	}
}

func TestRebuildAll_FromProviderSnapshot(t *testing.T) {
	cfg := makeBasicConfig()

	prov := newFakeProviderWith([]prober.Result{
		{
			Group:    "g2",
			Endpoint: "ep2",
			Protocol: "https",
			URL:      "https://svc.local",
			Route:    "routeB",
			Status:   "ok",
			Duration: 0.99,
			Err:      errors.New("ignored in labels but is_error=true"),
			At:       time.Unix(1700001000, 0),
			TLS: &validator.CertsReport{
				HadTLS:     true,
				ChainValid: false,
				Certificates: []validator.CertInfo{
					{
						Position:   0,
						SerialHex:  "1234",
						CommonName: "svc.local",
						IsCA:       false,
						IssuerCN:   "R12",
						DaysLeft:   42,
					},
				},
			},
		},
	})

	m := NewWDMetrics("prog", "ver", cfg, prov)
	t.Cleanup(func() { unregisterMetrics(m) })

	// When
	m.RebuildAll()

	// Then: timestamp (base labels)
	lblBase := prometheus.Labels{
		"group":    "g2",
		"endpoint": "ep2",
		"protocol": "https",
		"url":      "https://svc.local",
		"route":    "routeB",
	}
	if got := testutil.ToFloat64(m.EndpointLastProbeTimestamp.With(lblBase)); got != float64(time.Unix(1700001000, 0).Unix()) {
		t.Fatalf("timestamp mismatch, got %v", got)
	}

	// endpointResultLabels: TLS chain invalid → status must be "invalid-tls-chain"
	lblAll := prometheus.Labels{
		"group":    "g2",
		"endpoint": "ep2",
		"protocol": "https",
		"url":      "https://svc.local",
		"route":    "routeB",
		"status":   "invalid-tls-chain",
		"is_error": "true",
	}

	// EndpointValidation should be 1 for that status
	if got := testutil.ToFloat64(m.EndpointValidation.With(lblAll)); got != 1 {
		t.Fatalf("endpoint_validation mismatch, got %v want 1", got)
	}

	// Duration with the same labels
	if got := testutil.ToFloat64(m.EndpointDuration.With(lblAll)); got != 0.99 {
		t.Fatalf("duration mismatch, got %v want 0.99", got)
	}

	// Cert days left
	lblCert := prometheus.Labels{
		"group":          "g2",
		"endpoint":       "ep2",
		"protocol":       "https",
		"url":            "https://svc.local",
		"route":          "routeB",
		"cert_position":  "0",
		"cert_serial":    "1234",
		"cert_cn":        "svc.local",
		"cert_is_ca":     "false",
		"cert_issuer_cn": "R12",
	}
	if got := testutil.ToFloat64(m.EndpointTLSCertDaysLeft.With(lblCert)); got != 42 {
		t.Fatalf("cert days left mismatch, got %v want 42", got)
	}
}

// ---------- helpers ----------

func makeBasicConfig() *config.WatchDogConfig {
	return &config.WatchDogConfig{
		Metrics: config.MetricsContext{
			Namespace:   "ns",
			Environment: "env",
		},
		Settings: config.ProgramSettings{
			DefaultResponseBodyLimit: 1024,
			Debug:                    false,
			MaxWorkersCount:          1,
			DefaultTimeout:           500 * time.Millisecond,
		},
		Endpoints: map[string]config.Endpoint{},
		Routes:    map[string]config.Route{},
	}
}

type fakeProvider struct {
	results []prober.Result
}

func (f *fakeProvider) Snapshot() []prober.Result {
	// Return a copy to avoid test aliasing issues
	out := make([]prober.Result, len(f.results))
	copy(out, f.results)
	return out
}

func newFakeProvider() prober.Provider {
	return &fakeProvider{results: nil}
}

func newFakeProviderWith(results []prober.Result) prober.Provider {
	return &fakeProvider{results: results}
}

func unregisterMetrics(m *WDMetrics) {
	prometheus.Unregister(m.BuildInfo)
	prometheus.Unregister(m.EndpointValidation)
	prometheus.Unregister(m.EndpointDuration)
	prometheus.Unregister(m.EndpointLastProbeTimestamp)
	prometheus.Unregister(m.EndpointTLSCertDaysLeft)
}
