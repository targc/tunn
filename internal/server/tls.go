package server

import (
	"crypto/tls"
	"fmt"
	"net"
)

// loadTLSConfig loads a TLS certificate for termination mode.
func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS cert: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}

// terminateTLS performs the TLS handshake using the replayed ClientHello data
// and returns the unwrapped plaintext connection.
func terminateTLS(replayConn net.Conn, tlsCfg *tls.Config) (net.Conn, error) {
	tlsConn := tls.Server(replayConn, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("tls handshake failed: %w", err)
	}
	return tlsConn, nil
}
