# Wire Protocol

## Transport

Binary frames over WebSocket. All messages use binary WebSocket message type (opcode 0x02).

## Frame Format

```
+----------+----------+---------+
| Type     | StreamID | Payload |
| 1 byte   | 4 bytes  | N bytes |
| (uint8)  | (big-endian uint32) |
+----------+----------+---------+
```

Minimum frame size: 5 bytes (header only, no payload).

## Message Types

| Type | Value | Direction | Payload | Description |
|------|-------|-----------|---------|-------------|
| OpenStream | `0x01` | Server → Agent | Target address (UTF-8 string) | Request agent to open a connection to a K8s service |
| StreamReady | `0x02` | Agent → Server | (empty) | Agent successfully connected to target |
| Data | `0x03` | Bidirectional | Raw TCP bytes | Tunnel data for an active stream |
| CloseStream | `0x04` | Bidirectional | (empty) | Gracefully close a stream |
| StreamError | `0x05` | Bidirectional | Error message (UTF-8 string) | Report an error, stream is closed after this |

## Stream Lifecycle

```
Server                          Agent
  |                               |
  |--- OpenStream (id=1, addr) -->|  Server assigns stream ID, sends target
  |                               |  Agent dials K8s service
  |<-- StreamReady (id=1) --------|  Agent confirms connection
  |                               |
  |--- Data (id=1, bytes) ------->|  Bidirectional data flow
  |<-- Data (id=1, bytes) --------|
  |--- Data (id=1, bytes) ------->|
  |<-- Data (id=1, bytes) --------|
  |         ...                   |
  |                               |
  |--- CloseStream (id=1) ------->|  Either side can initiate close
  |    (or)                       |
  |<-- CloseStream (id=1) --------|
```

## Stream ID Allocation

- Server allocates stream IDs using an atomic uint32 counter starting at 1
- Only the server creates stream IDs (no collision possible)
- Stream ID 0 is reserved for control messages (future use)
- IDs are reusable after stream closes, but counter doesn't wrap in practice (4B+ connections)

## Error Handling

### StreamError
Sent when an error occurs on a specific stream (e.g. agent can't dial target). The stream is considered closed after sending/receiving StreamError. No CloseStream needed.

```
Server                          Agent
  |--- OpenStream (id=2, addr) -->|
  |<-- StreamError (id=2, msg) ---|  "dial tcp: connection refused"
  |  (stream 2 is now closed)     |
```

### WebSocket Disconnect
If the WebSocket connection drops:
- **Server**: closes all TCP client connections associated with active streams
- **Agent**: closes all K8s service connections, enters reconnect loop

All stream state is discarded. No attempt to resume streams across reconnections.

## Multiplexing

Multiple TCP connections are multiplexed over a single WebSocket connection using stream IDs. Each `Data` frame is tagged with its stream ID, allowing the receiver to route bytes to the correct TCP connection.

Example with 3 concurrent PostgreSQL connections:

```
WS frame: [0x03][0x00000001][...pg data...]   → stream 1
WS frame: [0x03][0x00000003][...pg data...]   → stream 3
WS frame: [0x03][0x00000001][...pg data...]   → stream 1
WS frame: [0x03][0x00000002][...pg data...]   → stream 2
```

## WebSocket Keepalive

Use WebSocket-level ping/pong (not application-level). Configure:
- Ping interval: 15 seconds
- Pong timeout: 10 seconds
- If pong not received, close connection and reconnect

## Authentication

Agent authenticates during WebSocket handshake:

```
GET /ws HTTP/1.1
Upgrade: websocket
Authorization: Bearer <agent-token>
```

Server validates token before completing upgrade. Invalid token → HTTP 401, connection closed.

## SNI + ALPN Peek Protocol

This is not part of the WebSocket protocol but documents how the server reads SNI/ALPN from incoming TCP:

1. Read up to 1024 bytes from TCP connection (peek, don't consume)
2. Parse TLS record header (5 bytes): content type (0x16 = handshake), version, length
3. Parse handshake header (4 bytes): type (0x01 = ClientHello), length
4. Skip: client version (2), random (32), session ID (variable), cipher suites (variable), compression (variable)
5. Parse extensions:
   - Type `0x0000` (server_name) → extract SNI hostname
   - Type `0x0010` (ALPN) → extract protocol list
6. Look up route by SNI hostname
7. If route has `alpn` configured, validate that at least one client ALPN protocol matches the route's allowed list. Reject if no match.

After peeking, wrap the connection so the peeked bytes are replayed on the next read.

Reject connection if:
- Not TLS (first byte != 0x16)
- No SNI extension
- No matching route for SNI
- Route has `alpn` configured but client ALPN doesn't match
