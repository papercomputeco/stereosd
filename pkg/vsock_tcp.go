package stereosd

import (
	"fmt"
	"log"
	"net"
)

// NewTCPListener creates a TCP listener on 0.0.0.0:<port>.
// This is used as the control plane listener when AF_VSOCK is unavailable
// (macOS/HVF development or when stereosd is started with --listen-mode tcp).
//
// The listener binds to all interfaces (0.0.0.0) rather than localhost because
// QEMU's user-mode networking (SLIRP) forwards packets from the host via the
// virtual gateway address, not from 127.0.0.1.
func NewTCPListener(port uint32) (VsockListener, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("vsock: starting TCP listener on %s", addr)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp listen %s: %w", addr, err)
	}

	return &tcpListener{listener: listener}, nil
}

// tcpListener wraps a TCP net.Listener to implement VsockListener.
type tcpListener struct {
	listener net.Listener
}

func (t *tcpListener) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *tcpListener) Close() error              { return t.listener.Close() }
func (t *tcpListener) Addr() net.Addr            { return t.listener.Addr() }
