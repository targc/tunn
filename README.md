# tunn

TCP tunnel for exposing Kubernetes services through a public endpoint. Routes traffic based on TLS SNI with optional ALPN validation.

## Architecture

```
TCP Client --> [tunn-server :6060] --WebSocket--> [tunn-agent in K8s] --> K8s Service
               (peek SNI, lookup route)           (dial service, proxy)
```

- **tunn-server** runs on a public VM, accepts TCP connections, peeks TLS ClientHello for SNI/ALPN, looks up the route, and forwards traffic through a WebSocket tunnel.
- **tunn-agent** runs as a pod inside a K8s cluster, maintains a persistent WebSocket connection to the server, dials target K8s services, and proxies traffic bidirectionally.

## Features

- Generic TCP tunneling with TLS passthrough or termination
- SNI-based routing to K8s services
- Optional ALPN validation per route (e.g. `postgresql`, `h2`)
- Per-route TLS mode: `passthrough` (end-to-end TLS) or `terminate` (server decrypts, forwards plaintext)
- PostgreSQL SSLRequest auto-detection
- Multiple concurrent connections multiplexed over a single WebSocket
- Multi-cluster support — multiple agents from different clusters
- Route config via YAML file or PostgreSQL database
- Auto-reconnect with exponential backoff

## Quick Start

### Build

```bash
go build -o tunn-server ./cmd/server
go build -o tunn-agent ./cmd/agent
```

### Run Server

```bash
# With YAML routes
TUNN_AGENT_TOKEN=secret TUNN_ROUTES_PATH=routes.yaml ./tunn-server

# With PostgreSQL routes
TUNN_AGENT_TOKEN=secret TUNN_DATABASE_URL="postgres://user:pass@host:5432/db" ./tunn-server
```

### Run Agent

```bash
TUNN_SERVER_URL=ws://server-host:6061/ws \
TUNN_AGENT_TOKEN=secret \
TUNN_CLUSTER_ID=cluster-a \
./tunn-agent
```

## Configuration

### Server Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TUNN_LISTEN` | no | `:6060` | TCP listener address |
| `TUNN_WS_LISTEN` | no | `:6061` | WebSocket listener for agents |
| `TUNN_AGENT_TOKEN` | yes | | Shared secret for agent auth |
| `TUNN_LOG_LEVEL` | no | `info` | Log level |
| `TUNN_ROUTES_PATH` | no* | | Path to YAML routes file |
| `TUNN_DATABASE_URL` | no* | | PostgreSQL connection string |
| `TUNN_TLS_CERT` | no** | | Path to TLS certificate (PEM) |
| `TUNN_TLS_KEY` | no** | | Path to TLS private key (PEM) |

\* At least one of `TUNN_ROUTES_PATH` or `TUNN_DATABASE_URL` must be set.

\*\* Required when any route uses `tls: terminate`. Supports wildcard certs (e.g. `*.tunn.example.com`).

### Agent Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TUNN_SERVER_URL` | yes | | WebSocket URL (e.g. `ws://host:6061/ws`) |
| `TUNN_AGENT_TOKEN` | yes | | Shared secret matching server |
| `TUNN_CLUSTER_ID` | yes | | Cluster identifier |
| `TUNN_LOG_LEVEL` | no | `info` | Log level |
| `TUNN_RECONNECT_MAX` | no | `30s` | Max reconnect backoff |

### Route Configuration

#### YAML

```yaml
routes:
  - domain: "test-pg-1.tcplb.example.com"
    service: "example-pg-1.default.svc.cluster.local:5432"
    cluster: "cluster-a"
    tls: terminate
    alpn:
      - postgresql

  - domain: "redis-1.tcplb.example.com"
    service: "redis-master.default.svc.cluster.local:6379"
    cluster: "cluster-b"
    # tls defaults to "passthrough"
```

#### PostgreSQL

```sql
CREATE TABLE routes (
    domain  VARCHAR(200) PRIMARY KEY,
    service VARCHAR(200) NOT NULL,
    cluster VARCHAR(100) NOT NULL,
    alpn    TEXT[],
    tls     VARCHAR(20) DEFAULT 'passthrough'
);

INSERT INTO routes (domain, service, cluster, tls)
VALUES ('test-pg-1.tcplb.example.com', 'example-pg-1.default.svc.cluster.local:5432', 'cluster-a', 'terminate');
```

#### TLS Modes

| Mode | Description | Backend requirement |
|------|-------------|-------------------|
| `passthrough` | TLS flows end-to-end to the backend | Backend must support TLS |
| `terminate` | Server decrypts TLS, forwards plaintext | No TLS needed on backend |

For `terminate` mode, generate a self-signed wildcard cert:

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -nodes -days 3650 -keyout certs/tunn.key -out certs/tunn.crt \
  -subj "/CN=*.tunn.example.com" \
  -addext "subjectAltName=DNS:*.tunn.example.com"
```

When using PostgreSQL, routes are queried on each new connection — changes take effect immediately.

## Docker

```bash
# Build
docker build -f Dockerfile.server -t tunn-server .
docker build -f Dockerfile.agent -t tunn-agent .

# Run server
docker run -p 6060:6060 -p 6061:6061 \
  -e TUNN_AGENT_TOKEN=secret \
  -e TUNN_DATABASE_URL="postgres://..." \
  tunn-server

# Run agent
docker run \
  -e TUNN_SERVER_URL=ws://server:6061/ws \
  -e TUNN_AGENT_TOKEN=secret \
  -e TUNN_CLUSTER_ID=cluster-a \
  tunn-agent
```

## Kubernetes Deployment (Agent)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tunn-agent
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tunn-agent
  template:
    metadata:
      labels:
        app: tunn-agent
    spec:
      containers:
        - name: tunn-agent
          image: tunn-agent:latest
          env:
            - name: TUNN_SERVER_URL
              value: "ws://tunnel-server.example.com:6061/ws"
            - name: TUNN_CLUSTER_ID
              value: "cluster-a"
            - name: TUNN_AGENT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tunn-agent-secret
                  key: token
```

## Project Structure

```
cmd/
  server/main.go              # Server entry point
  agent/main.go               # Agent entry point
internal/
  proto/proto.go              # Binary wire protocol (frame encode/decode)
  server/
    app.go                    # Server app bootstrap
    config.go                 # Server config (envconfig)
    server.go                 # TCP listener, WebSocket handler, stream proxy
    sni.go                    # TLS ClientHello SNI/ALPN parser + PostgreSQL SSLRequest
    alpn.go                   # ALPN validation
    tls.go                    # TLS termination
    route.go                  # Route model, IRouteManager interface
    route_postgres.go         # PostgreSQL route manager
    route_yaml.go             # YAML route manager
    stream.go                 # Stream manager (concurrent connections)
  agent/
    app.go                    # Agent app bootstrap
    config.go                 # Agent config (envconfig)
    agent.go                  # WebSocket client, reconnect, stream proxy
```
