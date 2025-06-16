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

func (m *WatchDogValidator) Validate(
	endpointName string,
	request config.EndpointRequest,
	routeName string,
	route config.Route,
	validation config.EndpointValidation,
) (valid bool, duration float64) {
	// prepare default HTTP client timeout
	client := &http.Client{
		Timeout: request.Timeout,
	}

	// parse request URL once
	targetURL := request.URL
	u, err := url.Parse(request.URL)
	if err != nil {
		log.Printf("failed to parse URL %s: %v", request.URL, err)
		return false, 0
	}
	originalHost := u.Hostname()

	// prepare proxy if needed
	var proxyFunc func(*http.Request) (*url.URL, error)
	if route.ProxyUrl != "" {
		proxyURL, err := url.Parse(route.ProxyUrl)
		if err != nil {
			log.Printf("failed to parse proxy URL %s: %v", route.ProxyUrl, err)
			return false, 0
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
		Proxy:           proxyFunc,
		TLSClientConfig: &tls.Config{ServerName: originalHost}, // SNI
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
		log.Printf("failed to prepare request for URL %s: %v", targetURL, err)
		return false, 0
	}

	// set Host header back to original
	req.Host = originalHost
	// set custom headers
	for key, val := range request.Headers {
		req.Header.Set(key, val)
	}

	// request
	start := time.Now()
	resp, err := client.Do(req)
	duration = time.Since(start).Seconds()
	if err != nil {
		if m.debug {
			log.Printf("request error for endpoint '%s' and route '%s': %v", endpointName, routeName, err)
		}
		return false, duration
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	// response validation
	valid = m.validateResponse(endpointName, routeName, resp, validation)
	return valid, duration
}

func (m *WatchDogValidator) validateResponse(endpointName, routeName string, resp *http.Response, v config.EndpointValidation) bool {
	if resp.StatusCode != v.StatusCode {
		if m.debug {
			log.Printf("wrong status code for endpoint '%s' and route '%s', expected '%d', got '%d'", endpointName, routeName, v.StatusCode, resp.StatusCode)
		}
		return false
	}

	for k, v := range v.Headers {
		gotV := resp.Header.Get(k)
		if gotV != v {
			if m.debug {
				log.Printf("wrong header for endpoint '%s' and route '%s', expected '%s', got '%s'", endpointName, routeName, v, gotV)
			}
			return false
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
				log.Printf("wrong body for endpoint '%s' and route '%s', expected regex '%s', got ---\n%s\n---", endpointName, routeName, v.BodyRegex, body)
			}
			return false
		}
	}
	return true
}

func NewWatchDogValidator(responseBodyLimit int64, debug bool) *WatchDogValidator {
	return &WatchDogValidator{responseBodyLimit: responseBodyLimit, debug: debug}
}
