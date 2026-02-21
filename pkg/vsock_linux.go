//go:build linux

// vsock_linux.go provides the real AF_VSOCK listener for Linux guests.
package stereosd

import (
	"fmt"
	"log"
	"net"
	"os"
	"syscall"
	"time"

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
// A real guest CID assigned by the hypervisor is always >= 3:
//   - CID 0 = VMADDR_CID_HYPERVISOR
//   - CID 1 = VMADDR_CID_LOCAL (reserved)
//   - CID 2 = VMADDR_CID_HOST
//   - CID 0xFFFFFFFF = VMADDR_CID_ANY (wildcard)
//
// When no transport is attached (no vhost-vsock-pci device), the ioctl
// returns CID 2 (host) rather than a guest CID, so checking != VMADDR_CID_ANY
// alone is insufficient. We require CID >= 3 to confirm a real transport.
func VsockTransportAvailable() bool {
	f, err := os.Open("/dev/vsock")
	if err != nil {
		log.Printf("vsock: /dev/vsock not available: %v", err)
		return false
	}
	defer f.Close()

	cid, err := unix.IoctlGetUint32(int(f.Fd()), unix.IOCTL_VM_SOCKETS_GET_LOCAL_CID)
	if err != nil {
		log.Printf("vsock: ioctl GET_LOCAL_CID failed: %v", err)
		return false
	}

	log.Printf("vsock: local CID = %d (0x%08X)", cid, cid)

	// Guest CIDs assigned by the hypervisor are >= 3. CID 0 (hypervisor),
	// 1 (local), 2 (host), and 0xFFFFFFFF (any) all indicate no real
	// guest transport is available.
	return cid >= 3 && cid != unix.VMADDR_CID_ANY
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

	// Wrap the raw fd in an *os.File for I/O. We cannot use net.FileConn
	// because Go's net package does not support AF_VSOCK sockets —
	// getsockname fails on them. Instead, vsockConn implements net.Conn
	// directly on top of the *os.File.
	file := os.NewFile(uintptr(nfd), "vsock")
	if file == nil {
		return nil, fmt.Errorf("vsock accept: invalid fd %d", nfd)
	}

	return &vsockConn{
		file:       file,
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

// vsockConn implements net.Conn over an AF_VSOCK socket using an *os.File
// for I/O. Go's net.FileConn does not support AF_VSOCK (getsockname fails),
// so we implement the interface directly.
type vsockConn struct {
	file       *os.File
	remoteAddr net.Addr
	localAddr  net.Addr
}

func (c *vsockConn) Read(b []byte) (int, error)         { return c.file.Read(b) }
func (c *vsockConn) Write(b []byte) (int, error)        { return c.file.Write(b) }
func (c *vsockConn) Close() error                       { return c.file.Close() }
func (c *vsockConn) RemoteAddr() net.Addr               { return c.remoteAddr }
func (c *vsockConn) LocalAddr() net.Addr                { return c.localAddr }
func (c *vsockConn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *vsockConn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *vsockConn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }
