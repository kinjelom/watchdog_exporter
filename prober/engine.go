package prober

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
	"watchdog_exporter/config"
	"watchdog_exporter/validator"
)

// Result represents one probe outcome for an endpoint+route.
type Result struct {
	Group    string
	Endpoint string
	Protocol string
	URL      string
	Route    string

	Status   string
	Duration float64
	Err      error

	TLS *validator.CertsReport

	// When the probe finished.
	At time.Time
}

// Provider exposes snapshots for passive readers (e.g., Prometheus exporter).
type Provider interface {
	Snapshot() []Result
}

// Subscriber will be notified for each Result (for push-style exporters).
type Subscriber interface {
	OnResult(Result)
}

// IntervalProvider lets you compute per-endpoint intervals from your config.
type IntervalProvider func(endpointName string, ep config.Endpoint) time.Duration

// Engine runs probing loops and fans out results.
type Engine struct {
	cfg       *config.WatchDogConfig
	validator *validator.WatchDogValidator

	intervalFor IntervalProvider
	store       *Store

	muSubs sync.RWMutex
	subs   []Subscriber

	// edge-triggered logging state: last error per key ("" means healthy)
	muErr       sync.Mutex
	lastResults map[string]string
}

// Store keeps the latest result per (group, endpoint, route, url, protocol).
type Store struct {
	mu    sync.RWMutex
	items map[string]Result // key -> last result
}

func NewStore() *Store {
	return &Store{items: make(map[string]Result)}
}

func (s *Store) keyOf(r Result) string {
	// Stable small cardinality key
	return r.Group + "\x00" + r.Endpoint + "\x00" + r.Route + "\x00" + r.Protocol + "\x00" + r.URL
}

func (s *Store) Put(r Result) {
	s.mu.Lock()
	s.items[s.keyOf(r)] = r
	s.mu.Unlock()
}

func (s *Store) Snapshot() []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Result, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}

func NewEngine(cfg *config.WatchDogConfig, v *validator.WatchDogValidator) *Engine {
	interval := func(_ string, _ config.Endpoint) time.Duration {
		return cfg.Settings.ProbeInterval
	}
	return &Engine{
		cfg:         cfg,
		validator:   v,
		intervalFor: interval,
		store:       NewStore(),
		lastResults: make(map[string]string),
	}
}

func (e *Engine) Provider() Provider { return e.store }

func (e *Engine) Subscribe(s Subscriber) {
	e.muSubs.Lock()
	defer e.muSubs.Unlock()
	e.subs = append(e.subs, s)
}

func (e *Engine) notify(r Result) {
	e.muSubs.RLock()
	defer e.muSubs.RUnlock()
	for _, s := range e.subs {
		// Subscribers must be fast or internally buffered.
		func(sub Subscriber, res Result) {
			defer func() { _ = recover() }()
			sub.OnResult(res)
		}(s, r)
	}
}

func (e *Engine) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for epName, ep := range e.cfg.Endpoints {
		epName, ep := epName, ep
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.runEndpointLoop(ctx, epName, ep)
		}()
	}
	<-ctx.Done()
	wg.Wait()
}

func (e *Engine) runEndpointLoop(ctx context.Context, endpointName string, endpoint config.Endpoint) {
	interval := e.intervalFor(endpointName, endpoint)
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Small jitter to avoid herd.
	jit := time.Duration(rand.Int63n(int64(interval / 10)))
	timer := time.NewTimer(jit)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			e.probeOnce(ctx, endpointName, endpoint)
			timer.Reset(interval)
		}
	}
}

// keyOf mirrors Store.keyOf without taking the Store lock.
// It must generate the same key as Store.keyOf.
func (e *Engine) keyOf(r Result) string {
	return r.Group + "\x00" + r.Endpoint + "\x00" + r.Route + "\x00" + r.Protocol + "\x00" + r.URL
}

// edge-triggered logging:
// - log when transitioning from healthy -> error, or when error message changes
// - log a single "recovered" when transitioning from error -> healthy
func (e *Engine) logOnTransition(r Result) {
	key := e.keyOf(r)

	e.muErr.Lock()
	defer e.muErr.Unlock()

	_, resExists := e.lastResults[key]
	prev := e.lastResults[key]
	cur := ""
	if r.Err != nil {
		cur = r.Err.Error()
	}

	switch {
	case !resExists:
		// first probe
		log.Printf("probe STARTED: group=%q endpoint=%q route=%q url=%q protocol=%q status=%s err=%q",
			r.Group, r.Endpoint, r.Route, r.URL, r.Protocol, r.Status, cur)
	case prev == "" && cur != "":
		// first error
		log.Printf("probe ERROR: group=%q endpoint=%q route=%q url=%q protocol=%q status=%s err=%q",
			r.Group, r.Endpoint, r.Route, r.URL, r.Protocol, r.Status, cur)
	case prev != "" && cur == "":
		// recovered
		log.Printf("probe RECOVERED: group=%q endpoint=%q route=%q url=%q protocol=%q status=%s",
			r.Group, r.Endpoint, r.Route, r.URL, r.Protocol, r.Status)
	case prev != "" && prev != cur:
		// error changed
		log.Printf("probe ERROR UPDATED: group=%q endpoint=%q route=%q url=%q protocol=%q status=%s err=%q (was %q)",
			r.Group, r.Endpoint, r.Route, r.URL, r.Protocol, r.Status, cur, prev)
	}
	e.lastResults[key] = cur
}

func (e *Engine) probeOnce(_ context.Context, endpointName string, endpoint config.Endpoint) {
	for _, routeKey := range endpoint.Routes {
		route := e.cfg.Routes[routeKey]

		status, duration, tlsRep, err := e.validator.Validate(
			endpointName, endpoint.Request, routeKey, route, endpoint.Validation, endpoint.InspectTLSCerts)

		res := Result{
			Group:    endpoint.Group,
			Endpoint: endpointName,
			Protocol: endpoint.Protocol,
			URL:      endpoint.Request.URL,
			Route:    routeKey,

			Status:   status,
			Duration: duration,
			Err:      err,
			TLS:      tlsRep,
			At:       time.Now(),
		}

		// Edge-triggered logging
		e.logOnTransition(res)

		// Save last state
		e.store.Put(res)
		// Fan out to subscribers (push exporters, logs, etc.)
		e.notify(res)
	}
}
