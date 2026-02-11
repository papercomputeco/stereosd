//go:build !linux

// vsock_other.go provides a fallback for non-Linux platforms (macOS, etc.).
// AF_VSOCK is a Linux-only feature. On other platforms, stereosd falls back to
// a TCP listener for development/testing purposes.
package stereosd

import (
	"fmt"
	"log"
	"net"
)

// NewRealVsockListener on non-Linux platforms creates a TCP listener
// as a development fallback. This allows running stereosd locally for testing.
func NewRealVsockListener(port uint32) (VsockListener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("vsock: AF_VSOCK not available, falling back to TCP %s (dev mode)", addr)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp fallback listen %s: %w", addr, err)
	}

	return &tcpVsockFallback{listener: listener}, nil
}

// tcpVsockFallback wraps a TCP listener to implement VsockListener.
type tcpVsockFallback struct {
	listener net.Listener
}

func (t *tcpVsockFallback) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *tcpVsockFallback) Close() error              { return t.listener.Close() }
func (t *tcpVsockFallback) Addr() net.Addr            { return t.listener.Addr() }
