package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/targc/tunn/internal/proto"
)

type agentConn struct {
	ws      *websocket.Conn
	writeCh chan []byte
}

type TunnelServer struct {
	config  *Config
	routes  IRouteManager
	streams *StreamManager
	tlsCfg  *tls.Config

	agents map[string]*agentConn
	mu     sync.RWMutex
}

func New(cfg *Config, routes IRouteManager, tlsCfg *tls.Config) *TunnelServer {
	return &TunnelServer{
		config:  cfg,
		routes:  routes,
		streams: NewStreamManager(),
		tlsCfg:  tlsCfg,
		agents:  make(map[string]*agentConn),
	}
}

func (s *TunnelServer) Start(ctx context.Context) error {
	go s.startWSServer(ctx)
	s.startDirectListeners(ctx)
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

func (s *TunnelServer) startDirectListeners(ctx context.Context) {
	routes, err := s.routes.ListenRoutes(ctx)
	if err != nil {
		slog.Error("failed to query listen routes", "err", err)
		return
	}

	for _, route := range routes {
		route := route
		go func() {
			ln, err := net.Listen("tcp", route.Listen)
			if err != nil {
				slog.Error("failed to start direct listener", "addr", route.Listen, "domain", route.Domain, "err", err)
				return
			}

			slog.Info("direct listener started", "addr", route.Listen, "domain", route.Domain, "target", route.Service)

			go func() {
				<-ctx.Done()
				ln.Close()
			}()

			for {
				conn, err := ln.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					slog.Error("direct accept error", "addr", route.Listen, "err", err)
					continue
				}
				go s.handleDirectConn(ctx, conn, &route)
			}
		}()
	}
}

func (s *TunnelServer) handleDirectConn(ctx context.Context, conn net.Conn, route *Route) {
	slog.Debug("direct connection", "addr", route.Listen, "domain", route.Domain, "remote", conn.RemoteAddr())

	proxyConn, err := s.resolveConn(conn, route)
	if err != nil {
		slog.Warn("resolve conn failed", "err", err, "domain", route.Domain)
		conn.Close()
		return
	}

	s.openStream(ctx, proxyConn, route)
}

func (s *TunnelServer) handleConn(ctx context.Context, conn net.Conn) {
	slog.Debug("new tcp connection", "remote", conn.RemoteAddr())

	info, replayConn, err := PeekClientHello(conn)
	if err != nil {
		slog.Warn("sni peek failed", "err", err, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	slog.Debug("tls client hello", "sni", info.SNI, "alpn", info.ALPN, "remote", conn.RemoteAddr())

	route, err := s.routes.LookupRoute(ctx, info.SNI)
	if err != nil {
		slog.Warn("no route", "sni", info.SNI, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	if err := validateALPN(route, info.ALPN); err != nil {
		slog.Warn("alpn rejected", "err", err, "sni", info.SNI, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}

	proxyConn, err := s.resolveConn(replayConn, route)
	if err != nil {
		slog.Warn("resolve conn failed", "err", err, "sni", info.SNI)
		conn.Close()
		return
	}

	s.openStream(ctx, proxyConn, route)
}

// resolveConn handles TLS termination or passthrough based on route config.
func (s *TunnelServer) resolveConn(conn net.Conn, route *Route) (net.Conn, error) {
	if route.TLS == "terminate" {
		if s.tlsCfg == nil {
			return nil, fmt.Errorf("tls terminate requested but no cert configured")
		}
		tlsConn, err := terminateTLS(conn, s.tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("tls termination failed: %w", err)
		}
		slog.Debug("tls terminated", "domain", route.Domain)
		return tlsConn, nil
	}
	return conn, nil
}

// openStream creates a stream, sends it to the agent, and starts proxying.
func (s *TunnelServer) openStream(ctx context.Context, conn net.Conn, route *Route) {
	agent := s.getAgent(route.Cluster)
	if agent == nil {
		slog.Warn("no agent for cluster", "cluster", route.Cluster, "domain", route.Domain)
		conn.Close()
		return
	}

	stream := s.streams.Create(conn, route.Service, route.Cluster)
	slog.Info("stream opened", "id", stream.ID, "domain", route.Domain, "target", route.Service, "cluster", route.Cluster)

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

	go func() {
		select {
		case <-stream.Ready:
			slog.Info("stream ready, proxying", "id", stream.ID, "domain", route.Domain, "target", route.Service)
			s.proxyClientToAgent(stream, agent)
		case <-ctx.Done():
			slog.Debug("stream cancelled before ready", "id", stream.ID)
			s.streams.Remove(stream.ID)
		}
	}()
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
			if stream, ok := s.streams.Get(frame.StreamID); ok {
				select {
				case <-stream.Ready:
					// already closed
				default:
					close(stream.Ready)
				}
			}

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
