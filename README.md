# ea-ntfy

A lightweight Go service that bridges [ElastAlert2](https://elastalert2.readthedocs.io) webhook alerts to [ntfy](https://ntfy.sh) push notifications.

```
ElastAlert2 ──► POST /webhook ──► ea-ntfy ──► ntfy topic ──► your phone/desktop
```

---

## Features

- Accepts JSON alert payloads from ElastAlert2's `http_post` alerter
- Renders notifications using a customizable Go text template
- Maps alert severity to ntfy priority (`critical` → high, `warning` → default, `info` → low)
- Sets ntfy title and tags automatically from alert fields
- Configurable via environment variables — no config files needed
- Supports authenticated ntfy servers (self-hosted)
- HTTP proxy support via standard `HTTP_PROXY` / `HTTPS_PROXY` env vars
- `/health` endpoint for container health checks

---

## Quick Start

### 1. Configure environment variables

Edit `.env` in the project root:

```env
NTFY_URL=https://ntfy.sh          # or your self-hosted ntfy URL
NTFY_TOPIC=my-alerts              # ntfy topic name
NTFY_AUTH=Bearer <your-token>     # optional — for protected topics
NTFY_PRIORITY=default             # default ntfy priority
```

### 2. Start with Docker Compose

```bash
docker compose up -d --build ea-ntfy
```

### 3. Verify it's running

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

---

## Configuration

| Variable        | Default                    | Description                                              |
|-----------------|----------------------------|----------------------------------------------------------|
| `LISTEN_ADDR`   | `:8080`                    | TCP address the HTTP server listens on                   |
| `NTFY_URL`      | `https://ntfy.sh`          | Base URL of the ntfy server                              |
| `NTFY_TOPIC`    | *(required)*               | ntfy topic to publish to                                 |
| `NTFY_AUTH`     | *(empty)*                  | Authorization header value (e.g. `Bearer <token>`)       |
| `NTFY_PRIORITY` | `default`                  | Fallback ntfy priority when severity is not recognized   |
| `TEMPLATE_PATH` | `/templates/default.tmpl`  | Path to the Go text template file                        |
| `HTTP_PROXY`    | *(empty)*                  | HTTP proxy for outbound ntfy requests                    |
| `HTTPS_PROXY`   | *(empty)*                  | HTTPS proxy for outbound ntfy requests                   |
| `NO_PROXY`      | *(empty)*                  | Comma-separated list of hosts to bypass the proxy        |

---

## API

### `POST /webhook`

Accepts a JSON body from ElastAlert2. All fields are optional — missing fields render as `—` in the notification.

**Expected fields:**

| Field       | Type            | Description                         |
|-------------|-----------------|-------------------------------------|
| `rule_name` | string          | Name of the ElastAlert2 rule        |
| `message`   | string          | Human-readable alert description    |
| `severity`  | string          | `critical` / `warning` / `info`     |
| `index`     | string          | Elasticsearch index name            |
| `num_hits`  | number / string | Number of matching documents        |

Any additional fields in the payload are accessible in the template via `{{ .All.field_name }}`.

**Example request:**

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "rule_name": "High Error Rate",
    "severity": "critical",
    "message": ">=5 ERROR events in 2 minutes",
    "index": "app-logs",
    "num_hits": 20
  }'
```

**Response:**

```json
{"status":"ok"}
```

### `GET /health`

Returns `200 OK` when the service is running.

```json
{"status":"ok"}
```

---

## Notification Template

Templates use [Go's `text/template`](https://pkg.go.dev/text/template) syntax.

**Available variables:**

| Variable         | Description                              |
|------------------|------------------------------------------|
| `{{ .RuleName }}` | Alert rule name                         |
| `{{ .Message }}`  | Alert message                           |
| `{{ .Severity }}` | Severity string                         |
| `{{ .Index }}`    | Elasticsearch index                     |
| `{{ .NumHits }}`  | Number of matched documents             |
| `{{ .All.* }}`    | Any field from the raw JSON payload     |

**Available functions:**

| Function          | Description          |
|-------------------|----------------------|
| `{{ toUpper . }}` | Convert to uppercase |
| `{{ toLower . }}` | Convert to lowercase |

**Custom template example:**

Create your own template and set `TEMPLATE_PATH` to point to it:

```
⚠️ {{ .RuleName }}
Index : {{ .Index }}
Hits  : {{ .NumHits }}
{{ .Message }}
```

---

## ElastAlert2 Rule Configuration

Add the following to any ElastAlert2 rule to send alerts to ea-ntfy:

```yaml
alert:
  - post

http_post_url: "http://ea-ntfy:8080/webhook"
http_post_headers:
  Content-Type: "application/json"
http_post_payload:
  num_hits: num_hits
http_post_static_payload:
  rule_name: "Your Rule Name"
  index: "app-logs"
  severity: "critical"
  message: "Alert description here"
```

---

## Severity → ntfy Priority Mapping

| Severity   | ntfy Priority |
|------------|---------------|
| `critical` | `high`        |
| `warning`  | `default`     |
| `info`     | `low`         |
| *(other)*  | `NTFY_PRIORITY` env var |

---

## Project Structure

```
ea-ntfy/
├── Dockerfile               # Multi-stage build (golang:alpine → alpine)
├── go.mod
├── main.go                  # HTTP server, ntfy forwarder, template engine
├── templates/
│   └── default.tmpl         # Default notification template
└── README.md
```
