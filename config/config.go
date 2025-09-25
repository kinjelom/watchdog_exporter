package config

import (
	"log"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type WatchDogConfig struct {
	Settings  ProgramSettings     `yaml:"settings"`
	Metrics   MetricsContext      `yaml:"metrics"`
	Routes    map[string]Route    `yaml:"routes"`
	Endpoints map[string]Endpoint `yaml:"endpoints"`
}

type ProgramSettings struct {
	ListenAddress            string        `yaml:"listen-address" default:":9321"`
	TelemetryPath            string        `yaml:"telemetry-path" default:"/metrics"`
	MaxWorkersCount          int           `yaml:"max-workers-count" default:"4"`
	ProbeInterval            time.Duration `yaml:"probe-interval" default:"1m"`
	DefaultTimeout           time.Duration `yaml:"default-timeout" default:"5s"`
	DefaultResponseBodyLimit int64         `yaml:"default-response-body-limit" default:"1024"`
	Debug                    bool          `yaml:"debug"`
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
	Group           string              `yaml:"group" default:"default"`
	Protocol        string              `yaml:"protocol" default:"http"`
	InspectTLSCerts bool                `yaml:"inspect-tls-certs" default:"false"`
	Routes          []string            `yaml:"routes" default:"[]"`
	Request         EndpointRequest     `yaml:"request"`
	Validation      *EndpointValidation `yaml:"validation"`
}
type EndpointRequest struct {
	Method            string            `yaml:"method" default:"GET"`
	Headers           map[string]string `yaml:"headers" default:"{}"`
	URL               string            `yaml:"url"`
	Timeout           time.Duration     `yaml:"timeout" default:"0s"`
	ResponseBodyLimit int64             `yaml:"response-body-limit" default:"0"`
}
type EndpointValidation struct {
	StatusCode int               `yaml:"status-code" default:"200"`
	Headers    map[string]string `yaml:"headers" default:"{}"`
	BodyRegex  string            `yaml:"body-regex" default:".*"`
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
	for name, endpoint := range c.Endpoints {
		if endpoint.Request.Timeout == 0 {
			endpoint.Request.Timeout = c.Settings.DefaultTimeout
		}
		if endpoint.Request.ResponseBodyLimit == 0 {
			endpoint.Request.ResponseBodyLimit = c.Settings.DefaultResponseBodyLimit
		}
		c.Endpoints[name] = endpoint
	}
}

func (c *WatchDogConfig) LogSummary() {
	var routeKeys []string
	for k := range c.Routes {
		routeKeys = append(routeKeys, k)
	}
	log.Printf("Monitored endpoints count: %d, with interval: %v, routes: %s", len(c.Endpoints), c.Settings.ProbeInterval, strings.Join(routeKeys, ", "))
}
