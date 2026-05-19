package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/targc/tunn/internal/config"
	"github.com/targc/tunn/internal/proto"
)

type TunnelServer struct {
	config  *config.ServerConfig
	routes  *RouteTable
	streams *StreamManager

	agentConn *websocket.Conn
	writeCh   chan []byte
	mu        sync.RWMutex
}

func New(cfg *config.ServerConfig) *TunnelServer {
	return &TunnelServer{
		config:  cfg,
		routes:  NewRouteTable(cfg.Routes),
		streams: NewStreamManager(),
		writeCh: make(chan []byte, 256),
	}
}

func (s *TunnelServer) Start(ctx context.Context) error {
	go s.startWSServer(ctx)
	return s.startTCPListener(ctx)
}

func (s *TunnelServer) startTCPListener(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.config.Server.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Server.Listen, err)
	}
	defer ln.Close()

	slog.Info("tcp listener started", "addr", s.config.Server.Listen)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("accept error", "err", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *TunnelServer) handleConn(ctx context.Context, conn net.Conn) {
	info, replayConn, err := PeekClientHello(conn)
	if err != nil {
		slog.Debug("sni peek failed", "err", err, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	entry, err := s.routes.Lookup(info.SNI)
	if err != nil {
		slog.Warn("no route", "sni", info.SNI, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	if err := s.routes.ValidateALPN(entry, info.ALPN); err != nil {
		slog.Warn("alpn rejected", "err", err, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	s.mu.RLock()
	agent := s.agentConn
	s.mu.RUnlock()

	if agent == nil {
		slog.Warn("no agent connected", "sni", info.SNI)
		conn.Close()
		return
	}

	stream := s.streams.Create(replayConn, entry.Service)
	slog.Info("stream opened", "id", stream.ID, "sni", info.SNI, "target", entry.Service)

	frame := proto.EncodeFrame(&proto.Frame{
		Type:     proto.MsgOpenStream,
		StreamID: stream.ID,
		Payload:  []byte(entry.Service),
	})

	select {
	case s.writeCh <- frame:
	default:
		slog.Error("write channel full, dropping stream", "id", stream.ID)
		s.streams.Remove(stream.ID)
		return
	}

	// read from TCP client, send Data frames to agent
	go s.proxyClientToAgent(stream)
}

func (s *TunnelServer) proxyClientToAgent(stream *Stream) {
	defer s.streams.Remove(stream.ID)

	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Conn.Read(buf)
		if n > 0 {
			frame := proto.EncodeFrame(&proto.Frame{
				Type:     proto.MsgData,
				StreamID: stream.ID,
				Payload:  buf[:n],
			})
			select {
			case s.writeCh <- frame:
			default:
				slog.Error("write channel full", "stream", stream.ID)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				slog.Debug("client read error", "stream", stream.ID, "err", err)
			}
			closeFrame := proto.EncodeFrame(&proto.Frame{
				Type:     proto.MsgCloseStream,
				StreamID: stream.ID,
			})
			select {
			case s.writeCh <- closeFrame:
			default:
			}
			return
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *TunnelServer) startWSServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleAgentWS)

	srv := &http.Server{Addr: s.config.Server.WSListen, Handler: mux}
	slog.Info("ws listener started", "addr", s.config.Server.WSListen)

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("ws server error", "err", err)
	}
}

func (s *TunnelServer) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	expected := "Bearer " + s.config.Server.AgentToken
	if token != expected {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "err", err)
		return
	}

	slog.Info("agent connected", "remote", r.RemoteAddr)

	s.mu.Lock()
	if s.agentConn != nil {
		s.agentConn.Close()
	}
	s.agentConn = ws
	s.mu.Unlock()

	// start write pump
	go s.wsWritePump(ws)

	// read from agent
	s.wsReadPump(ws)

	s.mu.Lock()
	if s.agentConn == ws {
		s.agentConn = nil
	}
	s.mu.Unlock()

	s.streams.CloseAll()
	slog.Info("agent disconnected")
}

func (s *TunnelServer) wsWritePump(ws *websocket.Conn) {
	for data := range s.writeCh {
		if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
			slog.Error("ws write error", "err", err)
			ws.Close()
			return
		}
	}
}

func (s *TunnelServer) wsReadPump(ws *websocket.Conn) {
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			slog.Debug("ws read error", "err", err)
			return
		}

		frame, err := proto.DecodeFrame(data)
		if err != nil {
			slog.Error("frame decode error", "err", err)
			continue
		}

		switch frame.Type {
		case proto.MsgStreamReady:
			slog.Debug("stream ready", "id", frame.StreamID)

		case proto.MsgData:
			stream, ok := s.streams.Get(frame.StreamID)
			if !ok {
				continue
			}
			if _, err := stream.Conn.Write(frame.Payload); err != nil {
				slog.Debug("client write error", "stream", frame.StreamID, "err", err)
				s.streams.Remove(frame.StreamID)
			}

		case proto.MsgCloseStream:
			slog.Debug("stream closed by agent", "id", frame.StreamID)
			s.streams.Remove(frame.StreamID)

		case proto.MsgStreamError:
			slog.Warn("stream error from agent", "id", frame.StreamID, "err", string(frame.Payload))
			s.streams.Remove(frame.StreamID)
		}
	}
}
