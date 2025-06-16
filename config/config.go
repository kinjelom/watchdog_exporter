package config

import (
	"gopkg.in/yaml.v3"
	"net/http"
	"os"
	"time"
)

type WatchDogConfig struct {
	Settings  ProgramSettings     `yaml:"settings"`
	Metrics   MetricsContext      `yaml:"metrics"`
	Routes    map[string]Route    `yaml:"routes"`
	Endpoints map[string]Endpoint `yaml:"endpoints"`
}

type ProgramSettings struct {
	ListenAddress     string        `yaml:"listen-address"`
	TelemetryPath     string        `yaml:"telemetry-path"`
	MaxWorkersCount   int           `yaml:"max-workers-count"`
	DefaultTimeout    time.Duration `yaml:"default-timeout"`
	ResponseBodyLimit int64         `yaml:"response-body-limit"`
	Debug             bool          `yaml:"debug"`
}

type MetricsContext struct {
	Namespace   string `yaml:"namespace"`
	Environment string `yaml:"environment"`
}
type Route struct {
	ProxyUrl string `yaml:"proxy-url"`
	TargetIP string `yaml:"target-ip"`
}

type Endpoint struct {
	Protocol   string             `yaml:"protocol"`
	Routes     []string           `yaml:"routes"`
	Request    EndpointRequest    `yaml:"request"`
	Validation EndpointValidation `yaml:"validation"`
}
type EndpointRequest struct {
	Timeout time.Duration     `yaml:"timeout"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	URL     string            `yaml:"url"`
}
type EndpointValidation struct {
	StatusCode int               `yaml:"status-code"`
	Headers    map[string]string `yaml:"headers"`
	BodyRegex  string            `yaml:"body-regex"`
}

func LoadConfig(path string) (*WatchDogConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config WatchDogConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	config.fillDefaults()
	return &config, nil
}

func (c *WatchDogConfig) fillDefaults() {
	if c.Settings.ListenAddress == "" {
		c.Settings.ListenAddress = ":9321"
	}
	if c.Settings.TelemetryPath == "" {
		c.Settings.TelemetryPath = "/metrics"
	}
	if c.Settings.MaxWorkersCount == 0 {
		c.Settings.MaxWorkersCount = 4
	}
	if c.Settings.DefaultTimeout == 0 {
		c.Settings.DefaultTimeout = 5 * time.Second
	}
	if c.Settings.ResponseBodyLimit == 0 {
		c.Settings.ResponseBodyLimit = 1024
	}

	for _, endpoint := range c.Endpoints {
		if endpoint.Request.Timeout == 0 {
			endpoint.Request.Timeout = c.Settings.DefaultTimeout
		}
		if endpoint.Request.Method == "" {
			endpoint.Request.Method = http.MethodGet
		}
	}
}
