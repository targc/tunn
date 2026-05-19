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
	"github.com/targc/tunn/internal/proto"
	"gorm.io/gorm"
)

type agentConn struct {
	ws      *websocket.Conn
	writeCh chan []byte
}

type TunnelServer struct {
	config  *Config
	db      *gorm.DB
	streams *StreamManager

	agents map[string]*agentConn
	mu     sync.RWMutex
}

func New(cfg *Config, db *gorm.DB) *TunnelServer {
	return &TunnelServer{
		config:  cfg,
		db:      db,
		streams: NewStreamManager(),
		agents:  make(map[string]*agentConn),
	}
}

func (s *TunnelServer) Start(ctx context.Context) error {
	go s.startWSServer(ctx)
	return s.startTCPListener(ctx)
}

func (s *TunnelServer) startTCPListener(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.config.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Listen, err)
	}
	defer ln.Close()

	slog.Info("tcp listener started", "addr", s.config.Listen)

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

	route, err := lookupRoute(ctx, s.db, info.SNI)
	if err != nil {
		slog.Warn("no route", "sni", info.SNI, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	if err := validateALPN(route, info.ALPN); err != nil {
		slog.Warn("alpn rejected", "err", err, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	agent := s.getAgent(route.Cluster)
	if agent == nil {
		slog.Warn("no agent for cluster", "cluster", route.Cluster, "sni", info.SNI)
		conn.Close()
		return
	}

	stream := s.streams.Create(replayConn, route.Service, route.Cluster)
	slog.Info("stream opened", "id", stream.ID, "sni", info.SNI, "target", route.Service, "cluster", route.Cluster)

	frame := proto.EncodeFrame(&proto.Frame{
		Type:     proto.MsgOpenStream,
		StreamID: stream.ID,
		Payload:  []byte(route.Service),
	})

	select {
	case agent.writeCh <- frame:
	default:
		slog.Error("write channel full, dropping stream", "id", stream.ID)
		s.streams.Remove(stream.ID)
		return
	}

	go s.proxyClientToAgent(stream, agent)
}

func (s *TunnelServer) getAgent(clusterID string) *agentConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[clusterID]
}

func (s *TunnelServer) proxyClientToAgent(stream *Stream, agent *agentConn) {
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
			case agent.writeCh <- frame:
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
			case agent.writeCh <- closeFrame:
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

	srv := &http.Server{Addr: s.config.WSListen, Handler: mux}
	slog.Info("ws listener started", "addr", s.config.WSListen)

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
	expected := "Bearer " + s.config.AgentToken
	if token != expected {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	clusterID := r.Header.Get("X-Cluster-ID")
	if clusterID == "" {
		http.Error(w, "missing X-Cluster-ID header", http.StatusBadRequest)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "err", err)
		return
	}

	slog.Info("agent connected", "cluster", clusterID, "remote", r.RemoteAddr)

	agent := &agentConn{
		ws:      ws,
		writeCh: make(chan []byte, 256),
	}

	s.mu.Lock()
	if old, ok := s.agents[clusterID]; ok {
		old.ws.Close()
	}
	s.agents[clusterID] = agent
	s.mu.Unlock()

	go s.wsWritePump(agent)

	s.wsReadPump(ws)

	s.mu.Lock()
	if s.agents[clusterID] == agent {
		delete(s.agents, clusterID)
	}
	s.mu.Unlock()

	close(agent.writeCh)
	s.streams.CloseByCluster(clusterID)
	slog.Info("agent disconnected", "cluster", clusterID)
}

func (s *TunnelServer) wsWritePump(agent *agentConn) {
	for data := range agent.writeCh {
		if err := agent.ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
			slog.Error("ws write error", "err", err)
			agent.ws.Close()
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
