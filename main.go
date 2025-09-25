package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"watchdog_exporter/config"
	"watchdog_exporter/metrics"
	"watchdog_exporter/prober"
	"watchdog_exporter/validator"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var ProgramVersion = "dev"

const (
	ProgramName = "watchdog_exporter"
)

var wdm *metrics.WDMetrics

func main() {
	configFile := flag.String("config", "config.yml", "Path to configuration YAML")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		panic(fmt.Errorf("cannot load --config=%s: %v", *configFile, err))
	}
	cfg.LogSummary()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tlsChecker := validator.NewDefaultTLSChecker(cfg.Settings.Debug)
	httpRespChecker := validator.NewDefaultHTTPResponseChecker(cfg.Settings.Debug)
	wdv := validator.NewWatchDogValidator(tlsChecker, httpRespChecker, cfg.Settings.Debug)

	engine := prober.NewEngine(cfg, wdv)
	// Metrics exporter: passive (Prometheus pulls), updates on events.
	wdm = metrics.NewWDMetrics(ProgramName, ProgramVersion, cfg, engine.Provider())
	// Subscribe metrics to live results
	engine.Subscribe(wdm)
	// Seed metrics from any pre-existing snapshot (optional).
	wdm.RebuildAll()

	// Start probing loops.
	go engine.Start(ctx)

	// Start HTTP
	http.Handle(cfg.Settings.TelemetryPath, promhttp.Handler())
	fmt.Printf("Starting %s v%s on %s%s\n", ProgramName, ProgramVersion, cfg.Settings.ListenAddress, cfg.Settings.TelemetryPath)
	err = http.ListenAndServe(cfg.Settings.ListenAddress, nil)
	if err != nil {
		panic(fmt.Errorf("cannot start server: %v", err))
	}
}
