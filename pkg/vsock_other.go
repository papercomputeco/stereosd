//go:build !linux

// vsock_other.go provides fallbacks for non-Linux platforms (macOS, etc.).
// AF_VSOCK is a Linux-only feature. On non-Linux platforms,
// NewRealVsockListener returns an error; callers should use NewTCPListener
// (from vsock_tcp.go) as the fallback.
package stereosd

import "fmt"

// NewRealVsockListener on non-Linux platforms returns an error since
// AF_VSOCK is not available. The listen mode dispatcher in
// createVsockListener will fall back to NewTCPListener when mode is "auto".
func NewRealVsockListener(port uint32) (VsockListener, error) {
	return nil, fmt.Errorf("AF_VSOCK not available on this platform")
}
