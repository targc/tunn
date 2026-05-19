# Deployment

## Overview

```
+---------------------------+          +---------------------------+
|  Public VM                |          |  K8s Cluster              |
|                           |          |                           |
|  tunn-server container    |<-- WS ---|  tunn-agent pod           |
|  ports: 6060, 6061       |          |  (Deployment, 1 replica)  |
|                           |          |                           |
+---------------------------+          +---------------------------+
```

## Tunnel Server (Public VM)

Runs as a Docker container on a VM with public IP.

### Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o tunn-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/tunn-server /usr/local/bin/
COPY config.yaml /etc/tunn/config.yaml
EXPOSE 6060 6061
ENTRYPOINT ["tunn-server"]
CMD ["--config", "/etc/tunn/config.yaml"]
```

### docker-compose.yaml

```yaml
services:
  tunn-server:
    build: .
    dockerfile: Dockerfile.server
    ports:
      - "6060:6060"   # TCP listener (external clients)
      - "6061:6061"   # WebSocket (agent connection)
    volumes:
      - ./config.yaml:/etc/tunn/config.yaml:ro
    environment:
      - TUNN_AGENT_TOKEN=your-secret-token
      - TUNN_LOG_LEVEL=info
    restart: unless-stopped
```

### DNS Setup

Point wildcard or specific domains to the VM's public IP:

```
test-pg-1.tcplb.nortezh.com  → <VM_PUBLIC_IP>
test-pg-2.tcplb.nortezh.com  → <VM_PUBLIC_IP>
*.tcplb.nortezh.com          → <VM_PUBLIC_IP>   (or wildcard)
```

## Tunnel Agent (K8s)

Runs as a Deployment inside the K8s cluster.

### K8s Manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tunn-agent
  namespace: default
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
          image: your-registry/tunn-agent:latest
          env:
            - name: TUNN_SERVER_URL
              value: "ws://tunnel-server.example.com:6061/ws"
            - name: TUNN_AGENT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tunn-agent-secret
                  key: token
            - name: TUNN_LOG_LEVEL
              value: "info"
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: v1
kind: Secret
metadata:
  name: tunn-agent-secret
  namespace: default
type: Opaque
stringData:
  token: "your-secret-token"
```

### Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o tunn-agent ./cmd/agent

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/tunn-agent /usr/local/bin/
ENTRYPOINT ["tunn-agent"]
```

## Network Requirements

| From | To | Port | Protocol | Purpose |
|------|----|------|----------|---------|
| Internet | VM | 6060 | TCP | Client connections (psql, etc.) |
| K8s agent pod | VM | 6061 | WebSocket (HTTP upgrade) | Tunnel control + data |
| K8s agent pod | K8s services | varies | TCP | Forward tunneled traffic |

The agent initiates outbound connections only — no inbound K8s firewall rules needed.

## Replacing Traefik

To migrate from Traefik:

1. Deploy tunn-server on the public VM with route config matching current IngressRouteTCP rules
2. Deploy tunn-agent in K8s
3. Verify tunnel works with a test connection
4. Update DNS to point to the VM (if different from current Traefik ingress IP)
5. Remove Traefik DaemonSet and IngressRouteTCP resources
