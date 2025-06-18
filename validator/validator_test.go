package validator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"watchdog_exporter/config"
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

			status, duration := v.Validate("ep", req, "rt", route, tt.validation)
			if status != tt.expectStatus {
				t.Errorf("%s: expected status=%v, got %v", tt.name, tt.expectStatus, status)
			}
			if duration < 0 {
				t.Errorf("%s: expected non-negative duration, got %v", tt.name, duration)
			}
		})
	}
}
