package main

import (
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"watchdog_exporter/config"
	"watchdog_exporter/metrics"
)

var ProgramVersion = "dev"

const (
	ProgramName = "watchdog_exporter"
)

func handler(w http.ResponseWriter, r *http.Request) {
	wdMetrics.Emit()
	promhttp.Handler().ServeHTTP(w, r)
}

var wdMetrics *metrics.WatchDogMetrics

func main() {
	configFile := flag.String("config", "config.yml", "Path to configuration YAML")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		panic(fmt.Errorf("cannot load --config=%s: %v", *configFile, err))
	}
	wdMetrics = metrics.NewWatchDogMetrics(ProgramName, ProgramVersion, cfg)

	http.HandleFunc(cfg.Settings.TelemetryPath, handler)

	fmt.Printf("Starting %s v%s on %s%s\n", ProgramName, ProgramVersion, cfg.Settings.ListenAddress, cfg.Settings.TelemetryPath)
	err = http.ListenAndServe(cfg.Settings.ListenAddress, nil)
	if err != nil {
		panic(fmt.Errorf("cannot start server: %v", err))
	}
}
