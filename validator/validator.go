package validator

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
	"watchdog_exporter/config"
)

type WatchDogValidator struct {
	tlsChecker      TLSChecker
	responseChecker HTTPResponseChecker
	debug           bool
}

func NewWatchDogValidator(tlsChecker TLSChecker, responseChecker HTTPResponseChecker, debug bool) *WatchDogValidator {
	return &WatchDogValidator{
		tlsChecker:      tlsChecker,
		responseChecker: responseChecker,
		debug:           debug,
	}
}

func (m *WatchDogValidator) Validate(endpointName string, rc config.EndpointRequest, routeName string, route config.Route, validation *config.EndpointValidation, checkCerts bool) (status string, duration float64, certsRep *CertsReport, err error) {
	client := &http.Client{
		Timeout: rc.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	u, err := url.Parse(rc.URL)
	if err != nil {
		log.Printf("invalid-url: failed to parse URL %s - %v", rc.URL, err)
		return "invalid-url", 0, nil, err
	}
	originalHost := u.Hostname()

	var proxyFunc func(*http.Request) (*url.URL, error)
	if route.ProxyUrl != "" {
		proxyURL, pErr := url.Parse(route.ProxyUrl)
		if pErr != nil {
			log.Printf("invalid-proxy-definition: failed to parse proxy URL %s - %v", route.ProxyUrl, pErr)
			return "invalid-proxy-definition", 0, nil, pErr
		}
		proxyFunc = http.ProxyURL(proxyURL)
	}

	dialer := &net.Dialer{Timeout: rc.Timeout, KeepAlive: 30 * time.Second}

	transport := &http.Transport{
		Proxy:             proxyFunc,
		DisableKeepAlives: true,
		TLSClientConfig:   m.tlsChecker.TLSClientConfigWithSNI(originalHost),
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return dialer.DialContext(ctx, network, addr)
			}
			if route.TargetIP != "" {
				host = route.TargetIP
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	client.Transport = transport

	targetURL := rc.URL
	if route.TargetIP != "" {
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

	req, err := http.NewRequest(rc.Method, targetURL, nil)
	if err != nil {
		log.Printf("invalid-request-definition: failed to prepare rc for endpoint %s URL %s - %v", endpointName, targetURL, err)
		return "invalid-request-definition", 0, nil, err
	}
	req.Host = originalHost
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range rc.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("X-Local-Time") == "" {
		req.Header.Set("X-Local-Time", time.Now().Format(time.RFC3339))
	}

	start := time.Now()
	resp, err := client.Do(req)
	duration = time.Since(start).Seconds()
	if err == nil {
		if checkCerts && req.URL.Scheme == "https" && resp != nil && resp.TLS != nil {
			rep := m.tlsChecker.Inspect(resp)
			certsRep = &rep
		}
	} else {
		if req.URL.Scheme == "https" {
			if st, ok := m.tlsChecker.CheckHandshakeError(err); ok {
				if m.debug {
					log.Printf("%s: %s / '%s': %v", st, rc.URL, routeName, err)
				}
				return st, duration, nil, err
			}
		}

		if isTimeoutErr(err) {
			if m.debug {
				log.Printf("request-execution-timeout: %s / '%s': %v", rc.URL, routeName, err)
			}
			return "request-execution-timeout", duration, nil, err
		}
		if m.debug {
			log.Printf("invalid-request-execution: %s / '%s': %v", rc.URL, routeName, err)
		}
		return "invalid-request-execution", duration, nil, err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	// HTTP response validation via injected checker
	status = "valid"
	if validation != nil {
		status, err = m.responseChecker.ValidateResponse(rc.URL, routeName, resp, rc.ResponseBodyLimit, *validation)
	}
	duration = time.Since(start).Seconds()
	return status, duration, certsRep, err
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	var nErr net.Error
	if errors.As(err, &nErr) && nErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var uErr *url.Error
	if errors.As(err, &uErr) && uErr.Timeout() {
		return true
	}
	return false
}
