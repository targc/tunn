# Configuration

## Tunnel Server

Config file: `config.yaml`

```yaml
server:
  # TCP listener for external clients
  listen: ":6060"

  # WebSocket listener for agent connection
  ws_listen: ":6061"

  # Shared secret for agent authentication
  agent_token: "your-secret-token"

  # Logging level: debug, info, warn, error
  log_level: "info"

# Route mappings: SNI domain → K8s service address
routes:
  - domain: "test-pg-1.tcplb.nortezh.com"
    service: "example-pg-1.default.svc.cluster.local:5432"
    alpn:
      - postgresql

  - domain: "test-pg-2.tcplb.nortezh.com"
    service: "example-pg-2.default.svc.cluster.local:5432"
    alpn:
      - postgresql

  - domain: "redis-1.tcplb.nortezh.com"
    service: "redis-master.default.svc.cluster.local:6379"

  - domain: "grpc-api.tcplb.nortezh.com"
    service: "grpc-api.default.svc.cluster.local:9090"
    alpn:
      - h2
```

### Route Fields

| Field | Required | Description |
|-------|----------|-------------|
| `domain` | yes | SNI hostname to match (exact match) |
| `service` | yes | K8s service address in format `<name>.<namespace>.svc.cluster.local:<port>` |
| `alpn` | no | List of ALPN protocols for TLS negotiation (e.g. `postgresql`, `h2`). If omitted, no ALPN filtering — any TCP traffic is accepted. |

### Server Environment Overrides

Environment variables override config file values:

| Env | Overrides | Example |
|-----|-----------|---------|
| `TUNN_LISTEN` | `server.listen` | `:6060` |
| `TUNN_WS_LISTEN` | `server.ws_listen` | `:6061` |
| `TUNN_AGENT_TOKEN` | `server.agent_token` | `secret` |
| `TUNN_CONFIG_PATH` | config file path | `/etc/tunn/config.yaml` |
| `TUNN_LOG_LEVEL` | `server.log_level` | `debug` |

## Tunnel Agent

Agent is configured entirely via environment variables (no config file needed):

| Env | Required | Default | Description |
|-----|----------|---------|-------------|
| `TUNN_SERVER_URL` | yes | — | WebSocket URL of tunnel server (e.g. `ws://tunnel-server:6061/ws`) |
| `TUNN_AGENT_TOKEN` | yes | — | Shared secret matching server config |
| `TUNN_LOG_LEVEL` | no | `info` | Logging level |
| `TUNN_RECONNECT_MAX` | no | `30s` | Max reconnect backoff interval |

## Example: Matching Traefik Reference

The reference Traefik config:

```yaml
# Traefik IngressRouteTCP
spec:
  entryPoints:
    - tcp
  routes:
    - match: HostSNI(`test-pg-1.tcplb.nortezh.com`)
      services:
        - name: example-pg-1
          port: 5432
```

Equivalent tunn config:

```yaml
routes:
  - domain: "test-pg-1.tcplb.nortezh.com"
    service: "example-pg-1.default.svc.cluster.local:5432"
    alpn:
      - postgresql
```

The key difference: Traefik uses K8s service discovery (just `name` + `port`) and a separate TLSOption CRD for ALPN, while tunn keeps it all in one route config. The `alpn` field replaces Traefik's TLSOption resource.
