package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/targc/tunn/internal/config"
	"github.com/targc/tunn/internal/proto"
)

type TunnelAgent struct {
	config  *config.AgentConfig
	streams map[uint32]net.Conn
	writeCh chan []byte
	mu      sync.RWMutex
}

func New(cfg *config.AgentConfig) *TunnelAgent {
	return &TunnelAgent{
		config:  cfg,
		streams: make(map[uint32]net.Conn),
		writeCh: make(chan []byte, 256),
	}
}

func (a *TunnelAgent) Run(ctx context.Context) error {
	maxBackoff, err := time.ParseDuration(a.config.ReconnectMax)
	if err != nil {
		maxBackoff = 30 * time.Second
	}

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := a.connect(ctx)
		if err != nil {
			slog.Error("connection failed", "err", err)
		}

		// clean up all streams on disconnect
		a.closeAllStreams()

		slog.Info("reconnecting", "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxBackoff)
	}
}

func (a *TunnelAgent) connect(ctx context.Context) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.config.AgentToken)

	ws, _, err := websocket.DefaultDialer.DialContext(ctx, a.config.ServerURL, header)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer ws.Close()

	slog.Info("connected to server", "url", a.config.ServerURL)

	// reset backoff on successful connect
	done := make(chan struct{})

	// write pump
	go func() {
		defer ws.Close()
		for {
			select {
			case data, ok := <-a.writeCh:
				if !ok {
					return
				}
				if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
					slog.Error("ws write error", "err", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// read pump
	defer close(done)
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("ws read error: %w", err)
		}

		frame, err := proto.DecodeFrame(data)
		if err != nil {
			slog.Error("frame decode error", "err", err)
			continue
		}

		switch frame.Type {
		case proto.MsgOpenStream:
			go a.handleOpenStream(frame.StreamID, string(frame.Payload))

		case proto.MsgData:
			a.mu.RLock()
			conn, ok := a.streams[frame.StreamID]
			a.mu.RUnlock()
			if ok {
				if _, err := conn.Write(frame.Payload); err != nil {
					slog.Debug("service write error", "stream", frame.StreamID, "err", err)
					a.removeStream(frame.StreamID)
				}
			}

		case proto.MsgCloseStream:
			slog.Debug("stream closed by server", "id", frame.StreamID)
			a.removeStream(frame.StreamID)

		case proto.MsgStreamError:
			slog.Warn("stream error from server", "id", frame.StreamID, "err", string(frame.Payload))
			a.removeStream(frame.StreamID)
		}
	}
}

func (a *TunnelAgent) handleOpenStream(streamID uint32, target string) {
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		slog.Error("failed to dial target", "stream", streamID, "target", target, "err", err)
		frame := proto.EncodeFrame(&proto.Frame{
			Type:     proto.MsgStreamError,
			StreamID: streamID,
			Payload:  []byte(err.Error()),
		})
		select {
		case a.writeCh <- frame:
		default:
		}
		return
	}

	a.mu.Lock()
	a.streams[streamID] = conn
	a.mu.Unlock()

	slog.Info("stream opened", "id", streamID, "target", target)

	// send ready
	readyFrame := proto.EncodeFrame(&proto.Frame{
		Type:     proto.MsgStreamReady,
		StreamID: streamID,
	})
	select {
	case a.writeCh <- readyFrame:
	default:
	}

	// proxy service → agent → server
	a.proxyServiceToServer(streamID, conn)
}

func (a *TunnelAgent) proxyServiceToServer(streamID uint32, conn net.Conn) {
	defer a.removeStream(streamID)

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frame := proto.EncodeFrame(&proto.Frame{
				Type:     proto.MsgData,
				StreamID: streamID,
				Payload:  buf[:n],
			})
			select {
			case a.writeCh <- frame:
			default:
				slog.Error("write channel full", "stream", streamID)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				slog.Debug("service read error", "stream", streamID, "err", err)
			}
			closeFrame := proto.EncodeFrame(&proto.Frame{
				Type:     proto.MsgCloseStream,
				StreamID: streamID,
			})
			select {
			case a.writeCh <- closeFrame:
			default:
			}
			return
		}
	}
}

func (a *TunnelAgent) removeStream(id uint32) {
	a.mu.Lock()
	if conn, ok := a.streams[id]; ok {
		conn.Close()
		delete(a.streams, id)
	}
	a.mu.Unlock()
}

func (a *TunnelAgent) closeAllStreams() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, conn := range a.streams {
		conn.Close()
		delete(a.streams, id)
	}
}
