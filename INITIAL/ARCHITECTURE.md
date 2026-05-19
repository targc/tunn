# Architecture

## Overview

**tunn** is a TCP tunnel system with two components:

- **Tunnel Server** — runs on a public VM, accepts incoming TCP connections, reads SNI to identify the target, forwards traffic to the agent via WebSocket.
- **Tunnel Agent** — runs as a K8s pod, maintains a persistent WebSocket connection to the server, receives forwarded traffic and routes it to K8s services.

This replaces Traefik's IngressRouteTCP + HostSNI routing with a simpler, self-contained tunnel.

## Data Flow

```
                          Internet (public)
                               |
                         +-----------+
  TCP Client (psql) ---->| Tunnel    |
  connects to            | Server    |
  :6060 with TLS SNI     | (VM)      |
                         +-----+-----+
                               |
                          WebSocket (persistent)
                          port :6061
                               |
                     +---------+---------+
                     |   Tunnel Agent    |
                     |   (K8s Pod)       |
                     +---------+---------+
                               |
                         K8s internal DNS
                               |
                     +---------+---------+
                     | K8s Service       |
                     | (e.g. pg:5432)    |
                     +-------------------+
```

## Connection Lifecycle

1. **TCP client** connects to tunnel server on `:6060` (e.g. `psql --host=test-pg-1.tcplb.nortezh.com --port=6060`)
2. **Server peeks** at the TLS ClientHello to extract the SNI hostname
3. **Server looks up** the route: `test-pg-1.tcplb.nortezh.com` → `example-pg-1.default.svc.cluster.local:5432`
4. **Server sends** an `OpenStream` message to the agent over WebSocket, including a unique stream ID and the target address
5. **Agent dials** the K8s service (`example-pg-1.default.svc.cluster.local:5432`)
6. **Agent replies** with `StreamReady`
7. **Bidirectional proxy**: Server relays TCP data ↔ WebSocket `Data` frames ↔ Agent relays to K8s service
8. **Close**: When either side disconnects, a `CloseStream` message is sent to clean up

## Key Design Decisions

### Generic TCP with TLS Passthrough

The server is protocol-agnostic — it tunnels raw TCP bytes. It does **not** terminate TLS. It peeks at the TLS ClientHello just to extract SNI (and optionally validate ALPN), then forwards the **entire raw TLS stream** (including the ClientHello bytes) to the agent. The backend K8s service handles TLS itself.

Each route can optionally specify `alpn` protocols. When configured, the server checks the ALPN extension in the ClientHello and only accepts connections with matching protocols. This allows the same server to handle PostgreSQL (`alpn: [postgresql]`), gRPC (`alpn: [h2]`), or any other TCP protocol — without any protocol-specific logic.

### Two Ports on Server

| Port | Purpose |
|------|---------|
| `:6060` | TCP listener for external clients (raw TCP, SNI-peeked) |
| `:6061` | HTTP server for agent WebSocket connection |

Separating ports avoids the complexity of demultiplexing HTTP upgrades from raw TCP on a single port.

### Single Agent Connection (v1)

v1 supports one agent connected at a time. The agent maintains a persistent WebSocket connection with automatic reconnection. If the agent disconnects, all active streams are torn down and clients get disconnected.

Future: support multiple agents for HA, with routing/load balancing.

### WebSocket for Tunnel Transport

WebSocket was chosen over gRPC or raw TCP because:
- Low overhead for binary data (just framing, no protobuf serialization per chunk)
- Works through firewalls and proxies
- Built-in TLS support
- Simple to implement in Go
- Same approach used by cloudflared and chisel

## Components

### Tunnel Server

Responsibilities:
- Listen for TCP connections on `:6060`
- Peek TLS ClientHello, extract SNI
- Look up route from config (domain → K8s service address)
- Assign stream ID, send `OpenStream` to agent
- Relay TCP ↔ WebSocket frames for each stream
- Accept agent WebSocket on `:6061`, authenticate with shared token

### Tunnel Agent

Responsibilities:
- Connect to server via WebSocket (with auth token)
- Reconnect automatically on disconnect (exponential backoff 1s→30s)
- On `OpenStream`: dial the K8s service address
- Relay WebSocket frames ↔ K8s service TCP connection
- Clean up streams on close

### Route Table

Simple YAML config mapping domains to K8s service addresses, with optional ALPN. Loaded by the server at startup.

```
domain: "test-pg-1.tcplb.nortezh.com"  →  service: "...:5432", alpn: [postgresql]
domain: "redis-1.tcplb.nortezh.com"    →  service: "...:6379"  (no alpn, any TCP)
```

## Concurrency Model

- **Server**: One goroutine per TCP client connection. One goroutine reading from agent WebSocket dispatching frames to streams. One write goroutine serializing all WebSocket writes (gorilla/websocket requires serialized writes).
- **Agent**: One goroutine reading from WebSocket dispatching to streams. Per stream: one goroutine reading from K8s service and sending Data frames. One write goroutine for WebSocket serialization.
- **Stream ID allocation**: Atomic uint32 counter on the server. Only server creates stream IDs → no collisions.
