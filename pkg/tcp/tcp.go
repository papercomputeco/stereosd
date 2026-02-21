// Package tcp provides a [listener.Listener] backed by a TCP socket.
//
// This is used as the control plane listener when AF_VSOCK is unavailable
// (macOS/HVF development or when stereosd is started with --listen-mode tcp).
package tcp

import (
	"fmt"
	"log"
	"net"
)

// tcpListener wraps a TCP [net.Listener].
type tcpListener struct {
	listener net.Listener
}

// NewListener creates a TCP listener on 0.0.0.0:<port>.
//
// The listener binds to all interfaces (0.0.0.0) rather than localhost because
// QEMU's user-mode networking (SLIRP) forwards packets from the host via the
// virtual gateway address, not from 127.0.0.1.
//
// This is useful for stereOS testing in development.
func NewListener(port uint32) (net.Listener, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("tcp: starting listener on %s", addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp listen %s: %w", addr, err)
	}

	return &tcpListener{listener: ln}, nil
}

func (t *tcpListener) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *tcpListener) Close() error              { return t.listener.Close() }
func (t *tcpListener) Addr() net.Addr            { return t.listener.Addr() }
