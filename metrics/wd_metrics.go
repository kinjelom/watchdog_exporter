package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"watchdog_exporter/config"
	"watchdog_exporter/prober"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// WDMetrics exposes endpoint validation and TLS certificate metrics.
type WDMetrics struct {
	cfg      *config.WatchDogConfig
	provider prober.Provider

	BuildInfo                  *prometheus.GaugeVec
	EndpointLastProbeTimestamp *prometheus.GaugeVec
	EndpointValidation         *prometheus.GaugeVec
	EndpointDuration           *prometheus.GaugeVec
	EndpointTLSCertDaysLeft    *prometheus.GaugeVec

	lastMu        sync.Mutex
	lastByKey     map[string]prometheus.Labels
	lastCertMu    sync.Mutex
	lastCertByKey map[string][]prometheus.Labels
}

func NewWDMetrics(programName, programVersion string, cfg *config.WatchDogConfig, provider prober.Provider) *WDMetrics {
	opts := func(name, help string, constantLabels *prometheus.Labels) prometheus.GaugeOpts {
		return prometheus.GaugeOpts{
			Namespace:   cfg.Metrics.Namespace,
			Name:        name,
			Help:        help,
			ConstLabels: *constantLabels,
		}
	}

	baseEndpointLabels := []string{"group", "endpoint", "protocol", "url", "route"}
	endpointResultLabels := []string{"group", "endpoint", "protocol", "url", "route", "status", "is_error"}
	certLabels := []string{
		"group", "endpoint", "protocol", "url", "route",
		"cert_position", "cert_serial", "cert_cn", "cert_is_ca", "cert_issuer_cn",
	}

	m := &WDMetrics{
		cfg:           cfg,
		provider:      provider,
		lastByKey:     make(map[string]prometheus.Labels),
		lastCertByKey: make(map[string][]prometheus.Labels),

		BuildInfo: promauto.NewGaugeVec(
			opts("build_info", "Program build information", &prometheus.Labels{
				"program_name":    programName,
				"program_version": programVersion,
			}),
			[]string{},
		),

		EndpointLastProbeTimestamp: promauto.NewGaugeVec(
			opts("endpoint_last_probe_timestamp_seconds", "Unix timestamp of the last probe", &prometheus.Labels{
				"environment": cfg.Metrics.Environment,
			}),
			baseEndpointLabels,
		),

		EndpointValidation: promauto.NewGaugeVec(
			opts("endpoint_validation", "Endpoint validation status (includes TLS error types)", &prometheus.Labels{
				"environment": cfg.Metrics.Environment,
			}),
			endpointResultLabels,
		),

		EndpointDuration: promauto.NewGaugeVec(
			opts("endpoint_duration_seconds", "Duration of endpoint test in seconds", &prometheus.Labels{
				"environment": cfg.Metrics.Environment,
			}),
			endpointResultLabels,
		),

		EndpointTLSCertDaysLeft: promauto.NewGaugeVec(
			opts("endpoint_tls_cert_days_left", "Days until certificate expiration (by chain position)", &prometheus.Labels{
				"environment": cfg.Metrics.Environment,
			}),
			certLabels,
		),
	}

	m.BuildInfo.With(nil).Set(1)
	return m
}

// OnResult updates all metrics for a single probe result.
func (m *WDMetrics) OnResult(r prober.Result) {
	isErr := "false"
	if r.Err != nil {
		isErr = "true"
	}

	lblBase := prometheus.Labels{
		"group":    r.Group,
		"endpoint": r.Endpoint,
		"protocol": r.Protocol,
		"url":      r.URL,
		"route":    r.Route,
	}
	m.EndpointLastProbeTimestamp.With(lblBase).Set(float64(r.At.Unix()))

	status := deriveStatus(r)
	lblAll := prometheus.Labels{
		"group":    r.Group,
		"endpoint": r.Endpoint,
		"protocol": r.Protocol,
		"url":      r.URL,
		"route":    r.Route,
		"status":   status,
		"is_error": isErr,
	}

	key := baseKeyOf(r)

	// Remove old metric series for this endpoint key.
	m.lastMu.Lock()
	if prev, ok := m.lastByKey[key]; ok {
		m.EndpointValidation.Delete(prev)
		m.EndpointDuration.Delete(prev)
	}
	m.lastByKey[key] = lblAll
	m.lastMu.Unlock()

	// Set new metrics.
	m.EndpointValidation.With(lblAll).Set(1)
	m.EndpointDuration.With(lblAll).Set(r.Duration)

	// Handle certificates (TLS chain details).
	if r.TLS != nil && r.TLS.HadTLS {
		m.lastCertMu.Lock()
		if prevs, ok := m.lastCertByKey[key]; ok {
			for _, pl := range prevs {
				m.EndpointTLSCertDaysLeft.Delete(pl)
			}
		}
		m.lastCertByKey[key] = m.buildAndSetCertSeries(r)
		m.lastCertMu.Unlock()
	} else {
		// No TLS: remove any previous certificate series.
		m.lastCertMu.Lock()
		if prevs, ok := m.lastCertByKey[key]; ok {
			for _, pl := range prevs {
				m.EndpointTLSCertDaysLeft.Delete(pl)
			}
			delete(m.lastCertByKey, key)
		}
		m.lastCertMu.Unlock()
	}
}

// buildAndSetCertSeries sets TLS certificate expiration metrics and returns the created label sets.
func (m *WDMetrics) buildAndSetCertSeries(r prober.Result) []prometheus.Labels {
	out := make([]prometheus.Labels, 0, len(r.TLS.Certificates))
	for _, c := range r.TLS.Certificates {
		lblCert := prometheus.Labels{
			"group":          r.Group,
			"endpoint":       r.Endpoint,
			"protocol":       r.Protocol,
			"url":            r.URL,
			"route":          r.Route,
			"cert_position":  strconv.Itoa(c.Position),
			"cert_serial":    c.SerialHex,
			"cert_cn":        c.CommonName,
			"cert_is_ca":     fmt.Sprintf("%v", c.IsCA),
			"cert_issuer_cn": c.IssuerCN,
		}
		m.EndpointTLSCertDaysLeft.With(lblCert).Set(c.DaysLeft)
		out = append(out, lblCert)
	}
	return out
}

// RebuildAll fully resets and rebuilds metrics from the provider snapshot.
func (m *WDMetrics) RebuildAll() {
	results := m.provider.Snapshot()

	m.EndpointValidation.Reset()
	m.EndpointDuration.Reset()
	m.EndpointLastProbeTimestamp.Reset()
	m.EndpointTLSCertDaysLeft.Reset()

	m.lastMu.Lock()
	m.lastByKey = make(map[string]prometheus.Labels)
	m.lastMu.Unlock()

	m.lastCertMu.Lock()
	m.lastCertByKey = make(map[string][]prometheus.Labels)
	m.lastCertMu.Unlock()

	for _, r := range results {
		m.OnResult(r)
	}
}

// Helpers

// baseKeyOf builds a unique key for a given endpoint ignoring status/is_error.
func baseKeyOf(r prober.Result) string {
	return r.Group + "\x00" + r.Endpoint + "\x00" + r.Protocol + "\x00" + r.URL + "\x00" + r.Route
}

// mapTLSErrorToStatus maps known TLS handshake error strings to a stable status.
func mapTLSErrorToStatus(errMsg string) string {
	errMsg = strings.ToLower(errMsg)
	switch {
	case strings.Contains(errMsg, "expired-cert-leaf"):
		return "expired-cert-leaf"
	case strings.Contains(errMsg, "invalid-tls-chain"):
		return "invalid-tls-chain"
	case strings.Contains(errMsg, "invalid-tls-hostname"):
		return "invalid-tls-hostname"
	case strings.Contains(errMsg, "invalid-tls-certificate"):
		return "invalid-tls-certificate"
	case strings.Contains(errMsg, "unknownauthorityerror"):
		return "invalid-tls-unknown-authority"
	case strings.Contains(errMsg, "certificateinvaliderror"):
		return "invalid-tls-certificate"
	case strings.Contains(errMsg, "hostnameerror"):
		return "invalid-tls-hostname"
	case strings.Contains(errMsg, "handshake"):
		return "invalid-tls-handshake"
	case strings.Contains(errMsg, "tls"):
		return "invalid-tls-other"
	default:
		return ""
	}
}

// deriveStatus determines the final validation status for a given probe result.
func deriveStatus(r prober.Result) string {
	// Prefer TLS-related classification.
	if r.TLS != nil {
		if !r.TLS.HadTLS {
			return "invalid-tls-missing"
		}
		if !r.TLS.ChainValid {
			return "invalid-tls-chain"
		}
	}

	if r.Err != nil {
		if tlsStatus := mapTLSErrorToStatus(r.Err.Error()); tlsStatus != "" {
			return tlsStatus
		}
	}

	if r.Status != "" {
		return r.Status
	}

	if r.Err != nil {
		return "unknown-error"
	}

	return "valid"
}
