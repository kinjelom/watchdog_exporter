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

func TestValidate_HTTP_SimpleMatrix(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		headers      map[string]string
		body         string
		bodyLimit    int64
		validation   *config.EndpointValidation
		expectStatus string
	}{
		{
			name:       "Success all match",
			statusCode: http.StatusOK,
			headers:    map[string]string{"X-Test": "value"},
			body:       "hello world",
			bodyLimit:  100,
			validation: &config.EndpointValidation{
				StatusCode: http.StatusOK,
				Headers:    map[string]string{"X-Test": "value"},
				BodyRegex:  "hello",
			},
			expectStatus: "valid",
		},
		{
			name:         "unexpected-status-code",
			statusCode:   http.StatusInternalServerError,
			bodyLimit:    10,
			validation:   &config.EndpointValidation{StatusCode: http.StatusOK},
			expectStatus: "unexpected-status-code",
		},
		{
			name:         "invalid header value",
			statusCode:   http.StatusOK,
			headers:      map[string]string{"X-Test": "bad"},
			bodyLimit:    10,
			validation:   &config.EndpointValidation{StatusCode: http.StatusOK, Headers: map[string]string{"X-Test": "good"}},
			expectStatus: "unexpected-header-value",
		},
		{
			name:         "Body regex mismatch",
			statusCode:   http.StatusOK,
			body:         "abcdef",
			bodyLimit:    100,
			validation:   &config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "xyz"},
			expectStatus: "unexpected-body-regex",
		},
		{
			name:         "Body limit prevents full match",
			statusCode:   http.StatusOK,
			body:         "abcdef",
			bodyLimit:    3,
			validation:   &config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "abcd"},
			expectStatus: "unexpected-body-regex",
		},
		{
			name:         "Body limit allows partial match",
			statusCode:   http.StatusOK,
			body:         "abcdef",
			bodyLimit:    3,
			validation:   &config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "abc"},
			expectStatus: "valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			req := config.EndpointRequest{
				URL:               srv.URL,
				Timeout:           2 * time.Second,
				ResponseBodyLimit: tt.bodyLimit,
				Method:            http.MethodGet,
				Headers:           map[string]string{},
			}
			route := config.Route{ProxyUrl: "", TargetIP: ""}

			tc := NewDefaultTLSChecker(false)
			hc := NewDefaultHTTPResponseChecker(false)
			v := NewWatchDogValidator(tc, hc, false)

			status, duration, rep, err := v.Validate("ep", req, "rt", route, tt.validation, false)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectStatus, status)
			assert.GreaterOrEqual(t, duration, 0.0)
			assert.Nil(t, rep)
		})
	}
}

func TestValidateTimeoutOnHeadersDelay(t *testing.T) {
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
	tc := NewDefaultTLSChecker(false)
	hc := NewDefaultHTTPResponseChecker(false)
	v := NewWatchDogValidator(tc, hc, false)

	status, dur, rep, err := v.Validate("ep", req, "rt", route, &config.EndpointValidation{StatusCode: http.StatusOK}, false)
	assert.Error(t, err)
	assert.Equal(t, "request-execution-timeout", status)
	upper := (timeout + 200*time.Millisecond).Seconds()
	assert.LessOrEqual(t, dur, upper)
	assert.Nil(t, rep)
}

func TestValidateTimeoutDuringBodyRead(t *testing.T) {
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
		URL:               srv.URL,
		Timeout:           timeout,
		ResponseBodyLimit: 1024,
		Method:            http.MethodGet,
		Headers:           map[string]string{},
	}
	route := config.Route{}
	tc := NewDefaultTLSChecker(false)
	hc := NewDefaultHTTPResponseChecker(false)
	v := NewWatchDogValidator(tc, hc, false)

	status, dur, rep, err := v.Validate("ep", req, "rt", route, &config.EndpointValidation{StatusCode: http.StatusOK, BodyRegex: "hello"}, false)
	assert.Error(t, err)
	assert.Equal(t, "request-execution-timeout", status)
	upper := (timeout + 200*time.Millisecond).Seconds()
	assert.LessOrEqual(t, dur, upper)
	assert.Nil(t, rep)
}
func TestHTTPResponseChecker_ValidateResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-A", "a")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "ping pong")
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	assert.NoError(t, err)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	checker := NewDefaultHTTPResponseChecker(false)

	// status mismatch
	st, err := checker.ValidateResponse(srv.URL, "r1", resp, 1024, config.EndpointValidation{StatusCode: http.StatusOK})
	assert.NoError(t, err)
	assert.Equal(t, "unexpected-status-code", st)

	// headers mismatch
	resp2, _ := http.Get(srv.URL)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp2.Body)
	st, err = checker.ValidateResponse(srv.URL, "r1", resp2, 1024, config.EndpointValidation{
		StatusCode: http.StatusCreated,
		Headers:    map[string]string{"X-A": "b"},
	})
	assert.NoError(t, err)
	assert.Equal(t, "unexpected-header-value", st)

	// body regex mismatch
	resp3, _ := http.Get(srv.URL)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp3.Body)
	st, err = checker.ValidateResponse(srv.URL, "r1", resp3, 1024, config.EndpointValidation{
		StatusCode: http.StatusCreated,
		BodyRegex:  "xyz",
	})
	assert.NoError(t, err)
	assert.Equal(t, "unexpected-body-regex", st)

	// all good
	resp4, _ := http.Get(srv.URL)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp4.Body)
	st, err = checker.ValidateResponse(srv.URL, "r1", resp4, 1024, config.EndpointValidation{
		StatusCode: http.StatusCreated,
		Headers:    map[string]string{"X-A": "a"},
		BodyRegex:  "ping",
	})
	assert.NoError(t, err)
	assert.Equal(t, "valid", st)
}
