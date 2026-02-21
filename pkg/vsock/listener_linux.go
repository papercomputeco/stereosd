//go:build linux

package vsock

import (
	"fmt"
	"log"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// vsockListener wraps a raw AF_VSOCK file descriptor as a [listener.Listener].
type vsockListener struct {
	fd   int
	port uint32
}

// NewListener creates a listener on AF_VSOCK at the given port.
// It binds to VMADDR_CID_ANY so it accepts connections from any CID (the host).
//
// The socket is created with SOCK_CLOEXEC to prevent fd leakage to child
// processes.
func NewListener(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
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

// TransportAvailable checks whether a real vsock transport is attached
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
func TransportAvailable() bool {
	f, err := os.Open(devVsockPath)
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
	// Accept4 with SOCK_CLOEXEC|SOCK_NONBLOCK gives us a non-blocking,
	// close-on-exec fd in a single syscall. The non-blocking flag is
	// required so that os.NewFile registers the fd with the runtime
	// netpoller, enabling SetDeadline support.
	nfd, sa, err := unix.Accept4(l.fd, unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
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
	// getsockname fails on them. Instead, conn implements net.Conn
	// directly on top of the *os.File.
	file := os.NewFile(uintptr(nfd), "vsock")
	if file == nil {
		unix.Close(nfd)
		return nil, fmt.Errorf("vsock accept: invalid fd %d", nfd)
	}

	return &conn{
		file:       file,
		remoteAddr: &Addr{CID: remoteCID, Port: remotePort},
		localAddr:  &Addr{CID: unix.VMADDR_CID_ANY, Port: l.port},
	}, nil
}

func (l *vsockListener) Close() error {
	return unix.Close(l.fd)
}

func (l *vsockListener) Addr() net.Addr {
	return &Addr{CID: unix.VMADDR_CID_ANY, Port: l.port}
}
