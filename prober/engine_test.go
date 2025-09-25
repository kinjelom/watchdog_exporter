package prober

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"watchdog_exporter/config"
	"watchdog_exporter/validator"

	"github.com/stretchr/testify/assert"
)

func TestStore_PutAndSnapshotKeying(t *testing.T) {
	s := NewStore()

	r1 := Result{Group: "g", Endpoint: "ep", Route: "r1", Protocol: "http", URL: "http://a", Status: "ok"}
	r2 := Result{Group: "g", Endpoint: "ep", Route: "r2", Protocol: "http", URL: "http://a", Status: "ok"}
	r1b := Result{Group: "g", Endpoint: "ep", Route: "r1", Protocol: "http", URL: "http://a", Status: "new"} // same key as r1

	s.Put(r1)
	s.Put(r2)
	s.Put(r1b)

	snap := s.Snapshot()
	// Expect 2 items: (g,ep,r1,http,http://a) and (g,ep,r2,http,http://a)
	assert.Len(t, snap, 2)

	// Ensure the r1 entry has been updated to "new"
	foundNew := false
	for _, v := range snap {
		if v.Route == "r1" && v.Status == "new" {
			foundNew = true
		}
	}
	assert.True(t, foundNew, "expected r1 entry to be updated with latest status")
}

// --- Engine tests ---

func TestEngine_StoresLatestResult(t *testing.T) {
	interval := 10 * time.Millisecond
	cfg := makeCfg(interval)

	// HTTP server returning 200 OK with "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// One endpoint, one route
	cfg.Endpoints["ep1"] = config.Endpoint{
		Group:    "grp",
		Protocol: "http",
		Request:  config.EndpointRequest{URL: srv.URL, Timeout: 250 * time.Millisecond, Method: http.MethodGet},
		Routes:   []string{"r1"},
		Validation: &config.EndpointValidation{
			StatusCode: http.StatusOK,
		},
		InspectTLSCerts: false,
	}
	cfg.Routes["r1"] = config.Route{}

	v := newValidator(false)
	e := NewEngine(cfg, v)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		e.Start(ctx)
		close(done)
	}()

	// Wait a bit for at least one probe cycle (jitter ≤ interval/10)
	time.Sleep(interval + 20*time.Millisecond)
	cancel()
	<-done

	// Should have exactly 1 latest result in store
	snap := e.Provider().Snapshot()
	assert.Len(t, snap, 1)
	got := snap[0]
	assert.Equal(t, "grp", got.Group)
	assert.Equal(t, "ep1", got.Endpoint)
	assert.Equal(t, "r1", got.Route)
	assert.Equal(t, srv.URL, got.URL)
	assert.Equal(t, "valid", got.Status) // DefaultHTTPResponseChecker returns "valid" for 200 and no header/body checks
	assert.GreaterOrEqual(t, got.Duration, 0.0)
}

func TestEngine_NotifiesSubscribers(t *testing.T) {
	interval := 10 * time.Millisecond
	cfg := makeCfg(interval)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
	}))
	defer srv.Close()

	cfg.Endpoints["ep"] = config.Endpoint{
		Group:           "g",
		Protocol:        "http",
		Request:         config.EndpointRequest{URL: srv.URL, Timeout: 250 * time.Millisecond, Method: http.MethodGet},
		Routes:          []string{"r"},
		Validation:      &config.EndpointValidation{StatusCode: http.StatusOK},
		InspectTLSCerts: false,
	}
	cfg.Routes["r"] = config.Route{}

	v := newValidator(false)
	e := NewEngine(cfg, v)

	sub := &chanSub{ch: make(chan Result, 10)}
	e.Subscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Start(ctx)

	// Wait for at least one notification
	select {
	case r := <-sub.ch:
		assert.Equal(t, "g", r.Group)
		assert.Equal(t, "ep", r.Endpoint)
		assert.Equal(t, "r", r.Route)
		assert.Equal(t, "valid", r.Status)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive any result from subscriber")
	}
	cancel()
}

func TestEngine_RespectsInterval(t *testing.T) {
	interval := 25 * time.Millisecond
	cfg := makeCfg(interval)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// fast response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg.Endpoints["ep"] = config.Endpoint{
		Group:           "g",
		Protocol:        "http",
		Request:         config.EndpointRequest{URL: srv.URL, Timeout: 250 * time.Millisecond, Method: http.MethodGet},
		Routes:          []string{"r"},
		Validation:      &config.EndpointValidation{StatusCode: http.StatusOK},
		InspectTLSCerts: false,
	}
	cfg.Routes["r"] = config.Route{}

	v := newValidator(false)
	e := NewEngine(cfg, v)

	sub := &chanSub{ch: make(chan Result, 10)}
	e.Subscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Start(ctx)

	// Collect two consecutive results for the same (group, endpoint, route)
	var t1, t2 time.Time
	for i := 0; i < 2; i++ {
		select {
		case r := <-sub.ch:
			if i == 0 {
				t1 = r.At
			} else {
				t2 = r.At
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for results")
		}
	}
	cancel()

	delta := t2.Sub(t1)
	// After an initial jitter, the loop resets the fixed interval; allow small scheduler jitter.
	assert.GreaterOrEqual(t, delta, interval-5*time.Millisecond, "expected at least ProbeInterval between successive probes")
}

func TestEngine_EmitsForAllRoutes(t *testing.T) {
	interval := 10 * time.Millisecond
	cfg := makeCfg(interval)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg.Routes["r1"] = config.Route{}
	cfg.Routes["r2"] = config.Route{}

	cfg.Endpoints["ep"] = config.Endpoint{
		Group:           "g",
		Protocol:        "http",
		Request:         config.EndpointRequest{URL: srv.URL, Timeout: 200 * time.Millisecond, Method: http.MethodGet},
		Routes:          []string{"r1", "r2"},
		Validation:      &config.EndpointValidation{StatusCode: http.StatusOK},
		InspectTLSCerts: false,
	}

	v := newValidator(false)
	e := NewEngine(cfg, v)

	sub := &chanSub{ch: make(chan Result, 10)}
	e.Subscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Start(ctx)

	// Expect to see results for both routes (the same cycle or consecutive cycles).
	seen := map[string]bool{}
	var mu sync.Mutex

	waitUntil := time.Now().Add(1 * time.Second)
	for time.Now().Before(waitUntil) {
		select {
		case r := <-sub.ch:
			mu.Lock()
			seen[r.Route] = true
			mu.Unlock()
			if seen["r1"] && seen["r2"] {
				cancel()
				goto DONE
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
DONE:
	cancel()

	assert.True(t, seen["r1"], "expected result for route r1")
	assert.True(t, seen["r2"], "expected result for route r2")
}

// --- helpers ---

func makeCfg(interval time.Duration) *config.WatchDogConfig {
	return &config.WatchDogConfig{
		Metrics: config.MetricsContext{
			Namespace:   "ns",
			Environment: "env",
		},
		Settings: config.ProgramSettings{
			ProbeInterval:            interval,
			DefaultTimeout:           500 * time.Millisecond,
			MaxWorkersCount:          4,
			DefaultResponseBodyLimit: 1024,
			Debug:                    false,
		},
		Endpoints: map[string]config.Endpoint{},
		Routes:    map[string]config.Route{},
	}
}

func newValidator(debug bool) *validator.WatchDogValidator {
	tc := validator.NewDefaultTLSChecker(debug)
	hc := validator.NewDefaultHTTPResponseChecker(debug)
	return validator.NewWatchDogValidator(tc, hc, debug)
}

type chanSub struct {
	ch chan Result
}

func (c *chanSub) OnResult(r Result) {
	select {
	case c.ch <- r:
	default:
		// drop on overflow to avoid deadlocks in tests
	}
}
