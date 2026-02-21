// Package vsock provides a [listener.Listener] and net.Conn implementation
// for virtio-vsock (AF_VSOCK) sockets.
//
// AF_VSOCK provides host <-> guest communication that works even when the
// guest network is disabled. The Listener binds to VMADDR_CID_ANY so it
// accepts connections from any CID (typically the host, CID 2).
//
// Go's net package does not support AF_VSOCK (net.FileConn fails because
// getsockname is unsupported for this address family). Instead, this
// package implements net.Conn directly on top of an *os.File wrapping the
// raw socket fd, which gives correct runtime poller integration and
// deadline support.
//
// This package is inspired by github.com/mdlayher/vsock's excellent MIT licensed package.
package vsock

import (
	"fmt"
	"net"
	"os"
	"time"
)

const (
	// devVsockPath is the location of /dev/vsock. It is exposed on both the
	// hypervisor and on virtual machines.
	devVsockPath = "/dev/vsock"

	// network is the vsock network reported in net.OpError.
	network = "vsock"
)

// Addr implements net.Addr for vsock addresses.
type Addr struct {
	CID  uint32
	Port uint32
}

func (a *Addr) Network() string { return network }

// String returns a human readable representation of the CID and port.
func (a *Addr) String() string {
	return fmt.Sprintf("vsock(%d:%d)", a.CID, a.Port)
}

// conn implements net.Conn over an AF_VSOCK socket using an *os.File for
// I/O. Go's net.FileConn does not support AF_VSOCK (getsockname fails), so
// we implement the interface directly.
//
// The underlying file descriptor must be set to non-blocking mode before
// wrapping with os.NewFile so the Go runtime poller is engaged. This is
// handled automatically by [listener.Accept].
type conn struct {
	file       *os.File
	remoteAddr net.Addr
	localAddr  net.Addr
}

func (c *conn) Read(b []byte) (int, error)         { return c.file.Read(b) }
func (c *conn) Write(b []byte) (int, error)        { return c.file.Write(b) }
func (c *conn) Close() error                       { return c.file.Close() }
func (c *conn) RemoteAddr() net.Addr               { return c.remoteAddr }
func (c *conn) LocalAddr() net.Addr                { return c.localAddr }
func (c *conn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }
