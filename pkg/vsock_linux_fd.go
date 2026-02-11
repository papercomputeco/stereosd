//go:build linux

package stereosd

import "os"

// newFileFromFD creates an *os.File from a raw file descriptor on Linux.
func newFileFromFD(fd int) *os.File {
	return os.NewFile(uintptr(fd), "vsock")
}
