package validator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"watchdog_exporter/config"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		headers      map[string]string
		body         string
		bodyLimit    int64
		validation   config.EndpointValidation
		expectStatus string
	}{
		{
			name:       "Success all match",
			statusCode: http.StatusOK,
			headers:    map[string]string{"X-Test": "value"},
			body:       "hello world",
			bodyLimit:  100,
			validation: config.EndpointValidation{
				StatusCode: http.StatusOK,
				Headers:    map[string]string{"X-Test": "value"},
				BodyRegex:  "hello",
			},
			expectStatus: "valid",
		},
		{
			name:         "invalid status code",
			statusCode:   http.StatusInternalServerError,
			headers:      nil,
			body:         "",
			bodyLimit:    10,
			validation:   config.EndpointValidation{StatusCode: http.StatusOK},
			expectStatus: "invalid-status-code",
		},
		{
			name:         "invalid header value",
			statusCode:   http.StatusOK,
			headers:      map[string]string{"X-Test": "bad"},
			body:         "",
			bodyLimit:    10,
			validation:   config.EndpointValidation{StatusCode: http.StatusOK, Headers: map[string]string{"X-Test": "good"}},
			expectStatus: "invalid-header-value",
		},
		{
			name:         "Body regex mismatch",
			statusCode:   http.StatusOK,
			headers:      nil,
			body:         "abcdef",
			bodyLimit:    100,
			validation:   config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "xyz"},
			expectStatus: "invalid-body-regex",
		},
		{
			name:         "Body limit prevents full match",
			statusCode:   http.StatusOK,
			headers:      nil,
			body:         "abcdef",
			bodyLimit:    3,
			validation:   config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "abcd"},
			expectStatus: "invalid-body-regex",
		},
		{
			name:         "Body limit allows partial match",
			statusCode:   http.StatusOK,
			headers:      nil,
			body:         "abcdef",
			bodyLimit:    3,
			validation:   config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "abc"},
			expectStatus: "valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// create test server
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			// prepare request and route
			req := config.EndpointRequest{
				URL:     srv.URL,
				Timeout: 2 * time.Second,
				Method:  http.MethodGet,
				Headers: map[string]string{},
			}
			route := config.Route{ProxyUrl: "", TargetIP: ""}

			// create validator with body limit
			v := NewWatchDogValidator(tt.bodyLimit, false)

			status, duration, err := v.Validate("ep", req, "rt", route, tt.validation)
			assert.NoError(t, err, "unexpected error")
			if status != tt.expectStatus {
				t.Errorf("%s: expected status=%v, got %v", tt.name, tt.expectStatus, status)
			}
			if duration < 0 {
				t.Errorf("%s: expected non-negative duration, got %v", tt.name, duration)
			}
		})
	}
}

func TestValidateTimeoutOnHeadersDelay(t *testing.T) {
	// server delays sending headers longer than the timeout
	delay := 300 * time.Millisecond
	timeout := 100 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	req := config.EndpointRequest{
		URL:     srv.URL,
		Timeout: timeout,
		Method:  http.MethodGet,
		Headers: map[string]string{},
	}
	route := config.Route{}
	v := NewWatchDogValidator(1024, false)

	status, dur, err := v.Validate("ep", req, "rt", route, config.EndpointValidation{StatusCode: http.StatusOK})
	assert.Error(t, err, "expected error")
	if status != "timeout" {
		// We expect the client timeout to trigger before headers are written
		t.Fatalf("expected timeout, got %s (duration=%.3fs)", status, dur)
	}
	// Duration should be close to timeout, and definitely not exceed it by a large margin.
	// Allow generous jitter for slow CI.
	upper := float64((timeout + 200*time.Millisecond).Seconds())
	if dur > upper {
		t.Fatalf("timeout not enforced quickly enough: duration=%.3fs > upper bound=%.3fs", dur, upper)
	}
}

func TestValidateTimeoutDuringBodyRead(t *testing.T) {
	// server sends headers fast but delays the body; validator must read body due to BodyRegex
	bodyDelay := 300 * time.Millisecond
	timeout := 100 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()

	req := config.EndpointRequest{
		URL:     srv.URL,
		Timeout: timeout,
		Method:  http.MethodGet,
		Headers: map[string]string{},
	}
	route := config.Route{}
	v := NewWatchDogValidator(1024, false)

	// BodyRegex forces reading some body; use a regex that matches what server will send.
	status, dur, err := v.Validate("ep", req, "rt", route, config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "hello"})
	assert.Error(t, err, "expected error")
	if status != "timeout" {
		t.Fatalf("expected timeout due to body read timeout, got %s (duration=%.3fs)", status, dur)
	}
	upper := float64((timeout + 200*time.Millisecond).Seconds())
	if dur > upper {
		t.Fatalf("timeout not enforced quickly enough during body read: duration=%.3fs > upper bound=%.3fs", dur, upper)
	}
}
