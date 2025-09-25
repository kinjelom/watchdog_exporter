# watchdog_exporter

`watchdog_exporter` continuously probes HTTP(S) endpoints over one or more **routes**
(e.g., direct, internal proxy, external proxy) and:

- validates responses (TLS, HTTP status code, headers, body regex),
- exposes Prometheus metrics for pass/fail and latency,
- optionally inspects TLS certificate chains and exposes certificate metadata (e.g., days left).

## How it works (quick tour)

- A scheduler runs probes every `probe-interval`.
- For each **endpoint × route** pair, the exporter performs an HTTP(S) request with the configured method, headers, and timeout.
- Validation checks:
  - **TLS**: presence, chain validity, hostname match; optional cert inspection.
  - **HTTP**: expected status code, headers, and body regex.
- Results are exported as Prometheus metrics (names are prefixed with your `metrics.namespace`).

## Usage

Using the example config [`config.example.yml`](config.example.yml):

- Listens on `:9321` and serves metrics at `/metrics`.
- Probes run every **2m30s**.
- Up to **4** concurrent workers, each with a **5s** timeout and **1 KiB** response body read limit.
- All metrics are stamped with namespace **`watchdog`** and constant label **`environment="dev"`**.
- Three routes:
  - `direct` (no proxy),
  - `internal` (override `target-ip` to `127.0.0.1`, preserving the `Host` header),
  - `external` (HTTP proxy at `1.2.3.4:8080`).
- Two example endpoints:
  1. `example.com` over `direct` and `external`, with **TLS certificates inspection enabled**, expects HTTP 200 and body regex `.*Wrong Domain.*`.
  2. `example.org` over all three routes (overridden timeout 10s), expects HTTP 200 and `.*Example Domain.*`.

## Prometheus metrics

All metrics use the namespace from `metrics.namespace`. Except `build_info`, metrics include a constant label `environment` from config.

### Build info

* `watchdog_build_info{program_name,program_version} = 1`
  Constant gauge set at startup.

### Endpoint probes

**Common labels (base):**
`group, endpoint, protocol, url, route`

**Result labels (superset):**
`group, endpoint, protocol, url, route, status, is_error`

* `watchdog_endpoint_last_probe_timestamp_seconds{…} = <unix_ts>`
  Unix timestamp of the last completed probe per endpoint/route.

* `watchdog_endpoint_validation{…, status, is_error} = 1`
  One series per last result. `status` values:

    * `valid` – validation passed.
    * `unexpected-status-code` - unexpected status code.
    * `unexpected-header-value` - unexpected header value.
    * `unexpected-body-regex` - unexpected body regex match.
    * `request-execution-error` - request execution error.
    * `request-execution-timeout` - request execution timeout.
    * `invalid-tls-missing` - HTTPS expected but no TLS observed.
    * `invalid-tls-chain` - TLS chain invalid.
    * `invalid-tls-hostname` - hostname/SAN mismatch.
    * `invalid-tls-certificate` - generic cert problem.
    * `invalid-tls-unknown-authority` - untrusted CA.
    * `invalid-tls-handshake` - handshake.
    * `invalid-tls-other` - other TLS error.
    * `expired-cert-leaf` - leaf cert expired.
    * `unknown-error` - non-TLS error and no explicit custom status.

  `is_error` is `"true"` if an error occurred, otherwise `"false"`.

* `watchdog_endpoint_duration_seconds{…, status, is_error} = <float_seconds>`
  End-to-end probe duration for the last result.

### TLS certificates (when `inspect-tls-certs: true` and TLS was used)

**Labels:**
`group, endpoint, protocol, url, route, cert_position, cert_serial, cert_cn, cert_is_ca, cert_issuer_cn`

* `watchdog_endpoint_tls_cert_days_left{…} = <days_left_float>`
  One series per certificate in the validated chain (`cert_position` = 0 for leaf).

## Example PromQL

* Current failing checks:

  ```promql
  watchdog_endpoint_validation{status!="valid"}
  ```

* Top 10 slowest checks:

  ```promql
  topk(10, watchdog_endpoint_duration_seconds{})
  ```

* Seven days left for leaf certs only:

  ```promql
  watchdog_endpoint_tls_cert_days_left{cert_position="0"}<=7
  ```

## Operational notes

* **Concurrency**: controlled by `max-workers-count`.
* **Timeouts**: per-endpoint via `request.timeout`; otherwise `settings.default-timeout`.
* **Body regex**: only the first `response-body-limit` bytes are read, per-endpoint; otherwise `settings.default-response-body-limit`.
* **Route behaviors**:

    * `target-ip`: overrides DNS while preserving `Host` header and TLS SNI.
    * `proxy-url`: proxies the request (HTTP proxy).

## Running

* Build the binary and run it with your YAML config (serve on `listen-address`, metrics at `telemetry-path`).
* Ensure Prometheus scrapes the exporter (default `:9321/metrics`).

## Troubleshooting

* Unexpected TLS statuses → check CA trust, SAN/hostname, and chain completeness.
* Repeated “invalid-tls-chain” with an internal proxy → the proxy may terminate TLS; set `protocol: http` for that route or enable end-to-end TLS.
* Body regex never matches → increase `response-body-limit`.


## Grafana dashboard

[grafana-dashboard.json](docs/grafana-dashboard.json)
![](docs/grafana-dashboard.png)