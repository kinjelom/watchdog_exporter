package validator

import (
	"io"
	"log"
	"net/http"
	"regexp"
	"watchdog_exporter/config"
)

// HTTPResponseChecker is responsible for validating HTTP response
// (status code, headers, optional body regex). It MUST NOT close resp.Body;
type HTTPResponseChecker interface {
	ValidateResponse(reqURL, routeName string, resp *http.Response, responseBodyLimit int64, v config.EndpointValidation) (status string, err error)
}

// DefaultHTTPResponseChecker is a production-ready implementation
// that mirrors the previous inline logic.
type DefaultHTTPResponseChecker struct {
	Debug bool
}

func NewDefaultHTTPResponseChecker(debug bool) *DefaultHTTPResponseChecker {
	return &DefaultHTTPResponseChecker{
		Debug: debug,
	}
}

func (c *DefaultHTTPResponseChecker) ValidateResponse(reqURL, routeName string, resp *http.Response, responseBodyLimit int64, v config.EndpointValidation) (string, error) {
	if resp.StatusCode != v.StatusCode {
		if c.Debug {
			log.Printf("unexpected-status-code: %s / '%s', expected '%d', got '%d'", reqURL, routeName, v.StatusCode, resp.StatusCode)
		}
		return "unexpected-status-code", nil
	}

	for k, expected := range v.Headers {
		got := resp.Header.Get(k)
		if got != expected {
			if c.Debug {
				log.Printf("unexpected-header-value: %s / '%s', expected '%s', got '%s'", reqURL, routeName, expected, got)
			}
			return "unexpected-header-value", nil
		}
	}

	if v.BodyRegex != "" {
		reader := io.LimitReader(resp.Body, responseBodyLimit)
		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			if isTimeoutErr(readErr) {
				if c.Debug {
					log.Printf("request-execution-timeout: %s / '%s', body read error: %v", reqURL, routeName, readErr)
				}
				return "request-execution-timeout", readErr
			}
			if c.Debug {
				log.Printf("request-execution-error: %s / '%s', body read error: %v", reqURL, routeName, readErr)
			}
			return "request-execution-error", readErr
		}
		matched, _ := regexp.Match(v.BodyRegex, body)
		if !matched {
			if c.Debug {
				log.Printf("unexpected-body-regex: %s / '%s', expected regex '%s', got ---\n%s\n---", reqURL, routeName, v.BodyRegex, body)
			}
			return "unexpected-body-regex", nil
		}
	}

	return "valid", nil
}
