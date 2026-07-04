# GDPR-Compliant Self-Hosted Website Analytics

**Date:** 2026-07-01  
**Status:** Draft  
**Scope:** Single site (tzro.dev marketing site on GitHub Pages)

---

## Overview

A lightweight, self-hosted analytics system that tracks acquisition (where visitors come from) and conversion (whether they take specific actions) for the tzro marketing site — without cookies, personal data, or consent banners.

Inspired by [Plausible Analytics](https://github.com/plausible/analytics), but stripped to the minimum: a single Go binary (~800 lines) running on a $4/mo DigitalOcean droplet, backed by SQLite.

## Goals

1. **Know where visitors come from** — referrer domains, UTM parameters, organic vs direct
2. **Know whether visitors convert** — CTA clicks, downloads, doc page visits
3. **GDPR-compliant by default** — no cookies, no personal data stored, no consent banner needed
4. **Zero external dependencies** — no third-party analytics providers, no CDNs, no tracking pixels from other services
5. **Operationally trivial** — single binary, single file DB, deploy with `scp`

## Non-Goals

- Engagement metrics (scroll depth, time on page, heatmaps)
- Multi-site / multi-tenant support
- Real-time streaming dashboard
- User segmentation or cohort analysis
- A/B testing infrastructure

---

## Architecture

A single Go binary (`tzro-analytics`) with three responsibilities:

```
GitHub Pages (tzro.dev)
    │
    │  <script defer src="https://analytics.tzro.dev/t.js">
    │
    ▼
┌─────────────────────────────────────────────┐
│  tzro-analytics (Go binary, DO droplet)     │
│                                             │
│  ① Collector   POST /api/event              │
│     Receives sendBeacon(), hashes IP+UA     │
│     with daily salt → anonymous visitor_id, │
│     writes to SQLite                        │
│                                             │
│  ② Query API   GET /api/stats               │
│     Serves aggregated metrics as JSON       │
│     (Basic Auth protected)                  │
│                                             │
│  ③ Dashboard   GET /                        │
│     Static HTML/JS embedded in binary       │
│     via embed.FS, calls Query API           │
│     (Basic Auth protected)                  │
│                                             │
└──────────────────┬──────────────────────────┘
                   │
                   ▼
            analytics.db (SQLite)
```

### Key Properties

- **Single binary** — `go build -o tzro-analytics`, deploy with `scp` + systemd
- **No CGO** — SQLite via `modernc.org/sqlite` (pure Go)
- **CORS-restricted** — only accepts events from `tzro.dev`
- **Reverse proxy** — Caddy handles TLS via Let's Encrypt (auto-provisioned)

---

## Data Model

### `events` — Raw event log

```sql
CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT    NOT NULL,                        -- ISO 8601, UTC
    visitor_id   TEXT    NOT NULL,                        -- SHA-256(ip + user_agent + daily_salt), truncated 16 chars
    page         TEXT    NOT NULL,                        -- e.g., "/", "/docs", "/docs#installation"
    referrer     TEXT,                                    -- cleaned referrer domain (e.g., "google.com")
    utm_source   TEXT,
    utm_medium   TEXT,
    utm_campaign TEXT,
    event_name   TEXT    NOT NULL DEFAULT 'pageview',     -- "pageview", "cta_click", "download"
    country      TEXT                                     -- 2-letter code, excluded from v1 (future: MaxMind GeoLite2)
);

CREATE INDEX idx_events_timestamp ON events(timestamp);
CREATE INDEX idx_events_page ON events(page);
CREATE INDEX idx_events_event_name ON events(event_name);
```

### `daily_salts` — Rotating hash salts

```sql
CREATE TABLE daily_salts (
    date  TEXT PRIMARY KEY,    -- "2026-07-01"
    salt  TEXT NOT NULL         -- crypto/rand 32-byte hex string
);
```

A new salt is generated at midnight UTC. Salts older than 48 hours are deleted. This ensures visitor IDs cannot be correlated across days.

### `sessions` — Derived table (materialized every 5 minutes)

```sql
CREATE TABLE sessions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    visitor_id       TEXT    NOT NULL,
    date             TEXT    NOT NULL,
    entry_page       TEXT    NOT NULL,
    exit_page        TEXT    NOT NULL,
    referrer         TEXT,
    utm_source       TEXT,
    page_count       INTEGER NOT NULL,
    has_conversion   INTEGER NOT NULL DEFAULT 0,
    duration_seconds INTEGER
);
```

Sessions are computed by grouping events by `visitor_id` within a 30-minute inactivity window. This is a background materialization, not a live table.

---

## GDPR Compliance

The system achieves GDPR compliance through data minimization:

| Concern | Approach |
|---------|----------|
| **Cookies** | None set. No localStorage or sessionStorage used. |
| **IP addresses** | Never stored. Hashed in-memory with daily rotating salt before any write. |
| **User-Agent** | Never stored. Used only as hash input, discarded after hashing. |
| **Fingerprinting** | No browser fingerprinting. Only IP + User-Agent used for daily unique counts. |
| **Cross-day tracking** | Impossible. Daily salt rotation means the same visitor gets a different ID each day. |
| **Consent banner** | Not required. No personal data is processed or stored. |
| **Data retention** | Raw events auto-deleted after 90 days. Aggregated daily summaries kept indefinitely. |
| **Third parties** | Zero. No data leaves the droplet. |

---

## Client-Side Tracking Script

### Inclusion on the website

```html
<script defer src="https://analytics.tzro.dev/t.js"></script>
```

### Script behavior (`t.js`, ~40 lines, <1KB minified)

```js
(function() {
  var endpoint = "https://analytics.tzro.dev/api/event";

  var params = new URLSearchParams(location.search);
  var utm = {
    source:   params.get("utm_source"),
    medium:   params.get("utm_medium"),
    campaign: params.get("utm_campaign")
  };

  function track(eventName) {
    var data = {
      page:         location.pathname,
      referrer:     document.referrer ? new URL(document.referrer).hostname : null,
      event:        eventName || "pageview",
      utm_source:   utm.source,
      utm_medium:   utm.medium,
      utm_campaign: utm.campaign
    };
    navigator.sendBeacon(endpoint, JSON.stringify(data));
  }

  // Auto-track pageview
  track("pageview");

  // Auto-bind conversion events via data-track attribute
  document.addEventListener("click", function(e) {
    var el = e.target.closest("[data-track]");
    if (el) track(el.getAttribute("data-track"));
  });

  // Expose for programmatic tracking
  window.tztrack = track;
})();
```

### Conversion tracking on the website

```html
<a href="/install" data-track="install_click" class="cta-button">Get Started</a>
<a href="/releases/latest" data-track="download">Download Binary</a>
```

### What the script does NOT do

- ❌ Set cookies
- ❌ Use localStorage / sessionStorage
- ❌ Fingerprint the browser
- ❌ Make third-party requests
- ❌ Modify the DOM
- ❌ Block page rendering (loaded with `defer`)

---

## Dashboard

The dashboard is a static HTML/JS page embedded in the Go binary via `embed.FS`, served at `GET /` behind HTTP Basic Auth.

### Panels

1. **KPI cards** — Unique visitors, page views, total conversions, conversion rate (with % change vs previous period)
2. **Visitors bar chart** — Daily bars for 7d/30d views, hourly bars for 24h view
3. **Top Referrers** — Ranked list of referrer domains with counts, including "(direct / none)"
4. **Top Pages** — Ranked list of pages by view count
5. **Conversions breakdown** — Count per `event_name` (install_click, download, github_click, etc.)

### Period selector

Toggle between 24h, 7d, 30d, and All time. Each period shows % change vs the equivalent previous period.

### Access control

- HTTP Basic Auth with credentials from environment variables (`ANALYTICS_AUTH_USER`, `ANALYTICS_AUTH_PASS`)
- The `/api/event` endpoint is public (accepts beacons) but CORS-restricted to `tzro.dev`
- All other endpoints (`/`, `/api/stats`) require authentication

---

## Query API

### `GET /api/stats`

Returns aggregated metrics for the requested period.

**Query parameters:**
- `period` — `24h`, `7d`, `30d`, `all` (default: `7d`)

**Response:**

```json
{
  "period": "7d",
  "unique_visitors": 1247,
  "page_views": 3891,
  "conversions": 89,
  "conversion_rate": 7.1,
  "change": {
    "unique_visitors": 14.2,
    "page_views": 8.1,
    "conversions": -3.0,
    "conversion_rate": -1.2
  },
  "chart": [
    { "date": "2026-06-25", "visitors": 156, "views": 487 },
    { "date": "2026-06-26", "visitors": 192, "views": 601 }
  ],
  "top_referrers": [
    { "referrer": "google.com", "count": 412 },
    { "referrer": "github.com", "count": 287 }
  ],
  "top_pages": [
    { "page": "/", "count": 1891 },
    { "page": "/docs", "count": 1204 }
  ],
  "conversions_breakdown": [
    { "event_name": "install_click", "count": 42 },
    { "event_name": "download", "count": 31 }
  ]
}
```

---

## Deployment

### Infrastructure

| Component | Choice | Cost |
|-----------|--------|------|
| Droplet | DigitalOcean, smallest ($4/mo), Ubuntu 24.04 | $4/mo |
| TLS | Caddy reverse proxy + Let's Encrypt | $0 |
| DNS | `analytics.tzro.dev` A record → droplet IP | $0 |
| Storage | SQLite (single file on droplet) | $0 |
| **Total** | | **$4/mo** |

### Deploy command

```makefile
deploy-analytics:
	GOOS=linux GOARCH=amd64 go build -o tzro-analytics ./cmd/analytics
	scp tzro-analytics root@your-droplet:/opt/tzro-analytics/
	ssh root@your-droplet 'systemctl restart tzro-analytics'
```

### systemd unit

```ini
[Unit]
Description=tzro analytics
After=network.target

[Service]
ExecStart=/opt/tzro-analytics/tzro-analytics
WorkingDirectory=/opt/tzro-analytics
Environment=ANALYTICS_AUTH_USER=admin
Environment=ANALYTICS_AUTH_PASS=your-password
Environment=ANALYTICS_DB_PATH=/opt/tzro-analytics/analytics.db
Environment=ANALYTICS_DOMAIN=tzro.dev
Restart=always

[Install]
WantedBy=multi-user.target
```

### Caddy config

```
analytics.tzro.dev {
    reverse_proxy localhost:8080
}
```

### Backup

- Daily cron: `sqlite3 analytics.db ".backup /backups/analytics-$(date +%Y%m%d).db"`
- Retain 30 days. Expected data volume: 1-5MB/month.

### Monitoring

- `GET /healthz` returns `200 OK`
- Free uptime monitor (UptimeRobot or cron `curl`) checks every 5 minutes

---

## Go Package Structure

```
cmd/analytics/
├── main.go              # Entry point, config loading, server startup
internal/analytics/
├── collector.go         # POST /api/event handler, IP hashing, event writing
├── dashboard.go         # GET / handler, embed.FS serving
├── db.go                # SQLite connection, schema migration, queries
├── hasher.go            # SHA-256 visitor ID generation, salt rotation
├── query.go             # GET /api/stats handler, aggregation queries
├── session.go           # Background session materialization goroutine
└── middleware.go         # CORS, Basic Auth, request logging
website/
├── analytics-dashboard/ # Static HTML/JS/CSS for the dashboard (embedded)
│   ├── index.html
│   └── dashboard.js
```

Estimated total: ~800 lines of Go + ~200 lines of dashboard HTML/JS.

---

## Data Retention & Cleanup

A background goroutine runs daily at 02:00 UTC:

1. **Delete raw events older than 90 days** — `DELETE FROM events WHERE timestamp < ?`
2. **Delete salts older than 48 hours** — `DELETE FROM daily_salts WHERE date < ?`
3. **Rebuild sessions table** — Re-materialize from remaining events
4. **VACUUM** — Reclaim disk space

Aggregated data (the sessions table) is kept indefinitely since it contains no reversible personal data.
