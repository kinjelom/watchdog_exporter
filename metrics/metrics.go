package metrics

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sync"
	"watchdog_exporter/config"
	"watchdog_exporter/validator"
)

type WatchDogMetrics struct {
	cfg                *config.WatchDogConfig
	validator          *validator.WatchDogValidator
	BuildInfo          *prometheus.GaugeVec
	EndpointValidation *prometheus.GaugeVec
	EndpointDuration   *prometheus.GaugeVec
}

func NewWatchDogMetrics(programName, programVersion string, config *config.WatchDogConfig) *WatchDogMetrics {
	opts := func(name, help string, constantLabels *prometheus.Labels) prometheus.GaugeOpts {
		return prometheus.GaugeOpts{
			Namespace:   config.Metrics.Namespace,
			Name:        name,
			Help:        help,
			ConstLabels: *constantLabels,
		}
	}
	endpointLabels := []string{"endpoint", "protocol", "url", "route", "valid"}
	metrics := &WatchDogMetrics{
		cfg:       config,
		validator: validator.NewWatchDogValidator(config.Settings.ResponseBodyLimit, config.Settings.Debug),
		BuildInfo: promauto.NewGaugeVec(
			opts("build_info", "Program build information", &prometheus.Labels{
				"program_name":    programName,
				"program_version": programVersion,
			}),
			[]string{},
		),
		EndpointValidation: promauto.NewGaugeVec(
			opts("endpoint_validation", "Whether the endpoint is valid", &prometheus.Labels{"environment": config.Metrics.Environment}),
			endpointLabels),
		EndpointDuration: promauto.NewGaugeVec(
			opts("endpoint_duration_seconds", "Duration of endpoint test in seconds", &prometheus.Labels{"environment": config.Metrics.Environment}),
			endpointLabels),
	}
	return metrics
}

func (m *WatchDogMetrics) Emit() {
	m.BuildInfo.With(nil).Set(1)

	var wg sync.WaitGroup
	sem := make(chan struct{}, m.cfg.Settings.MaxWorkersCount)
	for endpointName, e := range m.cfg.Endpoints {
		wg.Add(1)
		sem <- struct{}{}
		go func(endpoint config.Endpoint) {
			defer wg.Done()
			m.emitEndpointMetrics(endpointName, endpoint)
			<-sem
		}(e)
	}

	wg.Wait()
}

func (m *WatchDogMetrics) emitEndpointMetrics(endpointName string, endpoint config.Endpoint) {
	for _, routeKey := range endpoint.Routes {
		route := m.cfg.Routes[routeKey]
		valid, duration := m.validator.Validate(endpointName, endpoint.Request, routeKey, route, endpoint.Validation)
		labels := prometheus.Labels{"endpoint": endpointName, "protocol": endpoint.Protocol,
			"url": endpoint.Request.URL, "route": routeKey, "valid": fmt.Sprintf("%v", valid)}

		m.EndpointValidation.With(labels).Set(1)
		m.EndpointDuration.With(labels).Set(duration)
	}
}
