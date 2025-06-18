package validator

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
	"watchdog_exporter/config"
)

type WatchDogValidator struct {
	responseBodyLimit int64
	debug             bool
}

func (m *WatchDogValidator) Validate(endpointName string, request config.EndpointRequest, routeName string, route config.Route, validation config.EndpointValidation) (status string, duration float64) {
	// prepare default HTTP client timeout
	client := &http.Client{
		Timeout: request.Timeout,
	}

	// parse request URL once
	targetURL := request.URL
	u, err := url.Parse(request.URL)
	if err != nil {
		log.Printf("invalid-url: failed to parse URL %s - %v", request.URL, err)
		return "invalid-url", 0
	}
	originalHost := u.Hostname()

	// prepare proxy if needed
	var proxyFunc func(*http.Request) (*url.URL, error)
	if route.ProxyUrl != "" {
		proxyURL, err := url.Parse(route.ProxyUrl)
		if err != nil {
			log.Printf("invalid-proxy-definition: failed to parse proxy URL %s - %v", route.ProxyUrl, err)
			return "invalid-proxy-definition", 0
		}
		proxyFunc = http.ProxyURL(proxyURL)
	}

	// dialer with default timeouts
	dialer := &net.Dialer{
		Timeout:   request.Timeout,
		KeepAlive: 30 * time.Second,
	}

	// custom Transport: TLS + SNI + DialContext for TargetIP override and optional proxy
	transport := &http.Transport{
		Proxy:             proxyFunc,
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{ServerName: originalHost}, // SNI
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// addr == "hostname:port"
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				// fallback to default dial
				return dialer.DialContext(ctx, network, addr)
			}

			// override host if route.TargetIP provided
			if route.TargetIP != "" {
				host = route.TargetIP
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	client.Transport = transport

	// rebuild target URL if TargetIP override
	if route.TargetIP != "" {
		// keep scheme and port, override host part
		if u.Port() == "" {
			if u.Scheme == "https" {
				u.Host = net.JoinHostPort(route.TargetIP, "443")
			} else {
				u.Host = net.JoinHostPort(route.TargetIP, "80")
			}
		} else {
			u.Host = net.JoinHostPort(route.TargetIP, u.Port())
		}
		targetURL = u.String()
	}

	// prepare HTTP request
	method := request.Method
	req, err := http.NewRequest(method, targetURL, nil)
	if err != nil {
		log.Printf("invalid-request-definition: failed to prepare request for endpoint %s URL %s - %v", endpointName, targetURL, err)
		return "invalid-request-definition", 0
	}

	// set Host header back to original
	req.Host = originalHost
	// set custom headers
	req.Header.Set("Cache-Control", "no-cache")
	for key, val := range request.Headers {
		req.Header.Set(key, val)
	}

	// request
	start := time.Now()
	resp, err := client.Do(req)
	duration = time.Since(start).Seconds()
	if err != nil {
		if m.debug {
			log.Printf("invalid-request-execution: %s / '%s': %v", request.URL, routeName, err)
		}
		return "invalid-request-execution", duration
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	// response validation
	status = m.validateResponse(request.URL, routeName, resp, validation)
	return status, duration
}

func (m *WatchDogValidator) validateResponse(url, routeName string, resp *http.Response, v config.EndpointValidation) (status string) {
	if resp.StatusCode != v.StatusCode {
		if m.debug {
			log.Printf("invalid-status-code: %s / '%s', expected '%d', got '%d'", url, routeName, v.StatusCode, resp.StatusCode)
		}
		return "invalid-status-code"
	}

	for k, v := range v.Headers {
		gotV := resp.Header.Get(k)
		if gotV != v {
			if m.debug {
				log.Printf("invalid-header-value: %s / '%s', expected '%s', got '%s'", url, routeName, v, gotV)
			}
			return "invalid-header-value"
		}
	}

	if v.BodyRegex != "" {
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(resp.Body)
		reader := io.LimitReader(resp.Body, m.responseBodyLimit)
		body, _ := io.ReadAll(reader)
		matched, _ := regexp.Match(v.BodyRegex, body)
		if !matched {
			if m.debug {
				log.Printf("invalid-body-regex: %s / '%s', expected regex '%s', got ---\n%s\n---", url, routeName, v.BodyRegex, body)
			}
			return "invalid-body-regex"
		}
	}
	return "valid"
}

func NewWatchDogValidator(responseBodyLimit int64, debug bool) *WatchDogValidator {
	return &WatchDogValidator{responseBodyLimit: responseBodyLimit, debug: debug}
}
