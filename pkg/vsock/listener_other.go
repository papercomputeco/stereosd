//go:build !linux

package vsock

import (
	"fmt"
	"net"
)

// On non-Linux platforms, AF_VSOCK is not available. NewListener returns an
// error; callers should use the tcp sub-package as a development fallback.

// NewListener on non-Linux platforms returns an error since AF_VSOCK is
// a Linux-only feature.
func NewListener(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("AF_VSOCK not available on this platform")
}

// TransportAvailable on non-Linux platforms always returns false.
func TransportAvailable() bool {
	return false
}
