package server

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
)

type ClientHelloInfo struct {
	SNI  string
	ALPN []string
}

type replayConn struct {
	net.Conn
	reader io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func PeekClientHello(conn net.Conn) (*ClientHelloInfo, net.Conn, error) {
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read client hello: %w", err)
	}
	data := buf[:n]

	// Handle STARTTLS-style negotiation before the actual TLS ClientHello.
	// PostgreSQL: client sends SSLRequest (8 bytes), server responds 'S', then TLS begins.
	if isPostgresSSLRequest(data[:n]) {
		slog.Debug("postgres SSLRequest detected, responding S", "remote", conn.RemoteAddr())
		if _, err := conn.Write([]byte("S")); err != nil {
			return nil, nil, fmt.Errorf("failed to send SSLRequest response: %w", err)
		}
		n, err = conn.Read(buf)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read TLS hello after SSLRequest: %w", err)
		}
		data = buf[:n]
	}

	info, err := parseClientHello(data)
	if err != nil {
		return nil, nil, err
	}

	replay := &replayConn{
		Conn:   conn,
		reader: io.MultiReader(bytes.NewReader(data), conn),
	}

	return info, replay, nil
}

// isPostgresSSLRequest checks for PostgreSQL SSLRequest message.
// Format: 4-byte length (8) + 4-byte code (80877103 = 0x04d2162f)
func isPostgresSSLRequest(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	length := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	code := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
	return length == 8 && code == 80877103
}

func parseClientHello(data []byte) (*ClientHelloInfo, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("too short for TLS record")
	}
	if data[0] != 0x16 {
		return nil, fmt.Errorf("not a TLS handshake (type=0x%02x)", data[0])
	}

	recordLen := int(data[3])<<8 | int(data[4])
	body := data[5:]
	if len(body) < recordLen {
		// partial read is ok, we just parse what we have
	}
	if len(body) < 4 {
		return nil, fmt.Errorf("too short for handshake header")
	}
	if body[0] != 0x01 {
		return nil, fmt.Errorf("not a ClientHello (type=0x%02x)", body[0])
	}

	// skip handshake header (4 bytes: type + 3-byte length)
	pos := 4
	if pos+2 > len(body) {
		return nil, fmt.Errorf("too short for client version")
	}
	pos += 2 // client version

	if pos+32 > len(body) {
		return nil, fmt.Errorf("too short for random")
	}
	pos += 32 // random

	// session ID
	if pos+1 > len(body) {
		return nil, fmt.Errorf("too short for session ID length")
	}
	sessionIDLen := int(body[pos])
	pos += 1 + sessionIDLen

	// cipher suites
	if pos+2 > len(body) {
		return nil, fmt.Errorf("too short for cipher suites length")
	}
	cipherLen := int(body[pos])<<8 | int(body[pos+1])
	pos += 2 + cipherLen

	// compression methods
	if pos+1 > len(body) {
		return nil, fmt.Errorf("too short for compression length")
	}
	compLen := int(body[pos])
	pos += 1 + compLen

	// extensions
	if pos+2 > len(body) {
		return nil, fmt.Errorf("too short for extensions length")
	}
	extLen := int(body[pos])<<8 | int(body[pos+1])
	pos += 2

	info := &ClientHelloInfo{}
	extEnd := pos + extLen
	if extEnd > len(body) {
		extEnd = len(body)
	}

	for pos+4 <= extEnd {
		extType := int(body[pos])<<8 | int(body[pos+1])
		extDataLen := int(body[pos+2])<<8 | int(body[pos+3])
		pos += 4

		if pos+extDataLen > extEnd {
			break
		}

		switch extType {
		case 0x0000: // server_name
			info.SNI = parseSNI(body[pos : pos+extDataLen])
		case 0x0010: // ALPN
			info.ALPN = parseALPN(body[pos : pos+extDataLen])
		}

		pos += extDataLen
	}

	if info.SNI == "" {
		return nil, fmt.Errorf("no SNI found in ClientHello")
	}

	return info, nil
}

func parseSNI(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	listLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < listLen || listLen < 3 {
		return ""
	}
	// type (1 byte) + name length (2 bytes)
	if data[0] != 0x00 { // host_name type
		return ""
	}
	nameLen := int(data[1])<<8 | int(data[2])
	data = data[3:]
	if len(data) < nameLen {
		return ""
	}
	return string(data[:nameLen])
}

func parseALPN(data []byte) []string {
	if len(data) < 2 {
		return nil
	}
	listLen := int(data[0])<<8 | int(data[1])
	data = data[2:]
	if len(data) < listLen {
		return nil
	}
	data = data[:listLen]

	var protocols []string
	for len(data) > 0 {
		pLen := int(data[0])
		data = data[1:]
		if len(data) < pLen {
			break
		}
		protocols = append(protocols, string(data[:pLen]))
		data = data[pLen:]
	}
	return protocols
}
