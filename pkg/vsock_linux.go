//go:build linux

// vsock_linux.go provides the real AF_VSOCK listener for Linux guests.
package stereosd

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// vsockAddr implements net.Addr for vsock addresses.
type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a *vsockAddr) Network() string { return "vsock" }
func (a *vsockAddr) String() string  { return fmt.Sprintf("vsock(%d:%d)", a.cid, a.port) }

// vsockListener wraps a raw vsock file descriptor as a net.Listener.
type vsockListener struct {
	fd   int
	port uint32
}

// NewRealVsockListener creates a listener on AF_VSOCK at the given port.
// It binds to VMADDR_CID_ANY so it accepts connections from any CID (the host).
func NewRealVsockListener(port uint32) (VsockListener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}

	sa := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: port,
	}

	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock bind port %d: %w", port, err)
	}

	if err := unix.Listen(fd, syscall.SOMAXCONN); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock listen: %w", err)
	}

	return &vsockListener{fd: fd, port: port}, nil
}

// VsockTransportAvailable checks whether a real vsock transport is attached
// by querying the local CID via /dev/vsock. AF_VSOCK sockets can be created
// and bound even without a transport (e.g., when the kernel module is loaded
// but no vhost-vsock-pci device is present from the hypervisor). Without a
// transport, the listener will never receive connections.
//
// Returns true if a vsock transport is attached (local CID is a real guest
// CID, not VMADDR_CID_ANY).
func VsockTransportAvailable() bool {
	f, err := os.Open("/dev/vsock")
	if err != nil {
		return false
	}
	defer f.Close()

	// IOCTL_VM_SOCKETS_GET_LOCAL_CID returns the guest's CID.
	// If no transport is attached, the CID will be VMADDR_CID_ANY (0xFFFFFFFF).
	cid, err := unix.IoctlGetUint32(int(f.Fd()), unix.IOCTL_VM_SOCKETS_GET_LOCAL_CID)
	if err != nil {
		return false
	}

	return cid != unix.VMADDR_CID_ANY
}

func (l *vsockListener) Accept() (net.Conn, error) {
	nfd, sa, err := unix.Accept(l.fd)
	if err != nil {
		return nil, err
	}

	remoteCID := uint32(0)
	remotePort := uint32(0)
	if vsa, ok := sa.(*unix.SockaddrVM); ok {
		remoteCID = vsa.CID
		remotePort = vsa.Port
	}

	file := newConnFromFD(nfd)
	return &vsockConn{
		Conn:       file,
		remoteAddr: &vsockAddr{cid: remoteCID, port: remotePort},
		localAddr:  &vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port},
	}, nil
}

func (l *vsockListener) Close() error {
	return unix.Close(l.fd)
}

func (l *vsockListener) Addr() net.Addr {
	return &vsockAddr{cid: unix.VMADDR_CID_ANY, port: l.port}
}

// vsockConn wraps a net.Conn with vsock-specific address information.
type vsockConn struct {
	net.Conn
	remoteAddr net.Addr
	localAddr  net.Addr
}

func (c *vsockConn) RemoteAddr() net.Addr { return c.remoteAddr }
func (c *vsockConn) LocalAddr() net.Addr  { return c.localAddr }

// newConnFromFD creates a net.Conn from a raw file descriptor.
func newConnFromFD(fd int) net.Conn {
	f := newFileFromFD(fd)
	conn, err := net.FileConn(f)
	f.Close() // FileConn dups the fd
	if err != nil {
		// This shouldn't happen with a valid fd, but handle it gracefully
		return nil
	}
	return conn
}
