package vsock

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVsock(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vsock Suite")
}

var _ = Describe("Addr", func() {
	It("should return 'vsock' as the network", func() {
		a := &Addr{CID: 3, Port: 1024}
		Expect(a.Network()).To(Equal("vsock"))
	})

	It("should format as vsock(CID:Port)", func() {
		a := &Addr{CID: 3, Port: 1024}
		Expect(a.String()).To(Equal("vsock(3:1024)"))
	})

	It("should format zero values correctly", func() {
		a := &Addr{CID: 0, Port: 0}
		Expect(a.String()).To(Equal("vsock(0:0)"))
	})

	It("should format large CID values", func() {
		a := &Addr{CID: 4294967295, Port: 65535}
		Expect(a.String()).To(Equal("vsock(4294967295:65535)"))
	})

	It("should satisfy the net.Addr interface", func() {
		var _ net.Addr = (*Addr)(nil)
	})
})

var _ = Describe("conn", func() {
	It("should satisfy the net.Conn interface", func() {
		var _ net.Conn = (*conn)(nil)
	})

	It("should return the addresses it was created with", func() {
		local := &Addr{CID: 3, Port: 1024}
		remote := &Addr{CID: 2, Port: 5000}

		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()
		defer w.Close()

		c := &conn{file: r, localAddr: local, remoteAddr: remote}
		Expect(c.LocalAddr()).To(Equal(local))
		Expect(c.RemoteAddr()).To(Equal(remote))
	})

	It("should read and write through the underlying file", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())

		local := &Addr{CID: 3, Port: 1024}
		remote := &Addr{CID: 2, Port: 5000}

		writer := &conn{file: w, localAddr: local, remoteAddr: remote}
		reader := &conn{file: r, localAddr: remote, remoteAddr: local}

		msg := []byte("hello vsock")
		n, err := writer.Write(msg)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(len(msg)))

		buf := make([]byte, 64)
		n, err = reader.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("hello vsock"))

		Expect(writer.Close()).To(Succeed())
		Expect(reader.Close()).To(Succeed())
	})

	It("should return EOF after the write end is closed", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())

		local := &Addr{CID: 3, Port: 1024}
		remote := &Addr{CID: 2, Port: 5000}

		writer := &conn{file: w, localAddr: local, remoteAddr: remote}
		reader := &conn{file: r, localAddr: remote, remoteAddr: local}

		Expect(writer.Close()).To(Succeed())

		buf := make([]byte, 64)
		_, err = reader.Read(buf)
		Expect(err).To(Equal(io.EOF))

		Expect(reader.Close()).To(Succeed())
	})

	It("should support SetDeadline on a pipe file", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer w.Close()

		c := &conn{
			file:       r,
			localAddr:  &Addr{CID: 3, Port: 1024},
			remoteAddr: &Addr{CID: 2, Port: 5000},
		}
		defer c.Close()

		// Set deadline in the past — reads should fail immediately.
		err = c.SetReadDeadline(time.Now().Add(-1 * time.Second))
		Expect(err).NotTo(HaveOccurred())

		buf := make([]byte, 64)
		_, err = c.Read(buf)
		Expect(err).To(HaveOccurred())
		Expect(os.IsTimeout(err)).To(BeTrue())
	})

	It("should support SetWriteDeadline", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		c := &conn{
			file:       w,
			localAddr:  &Addr{CID: 3, Port: 1024},
			remoteAddr: &Addr{CID: 2, Port: 5000},
		}
		defer c.Close()

		// Setting a write deadline should not error.
		err = c.SetWriteDeadline(time.Now().Add(1 * time.Hour))
		Expect(err).NotTo(HaveOccurred())
	})

	It("should support SetDeadline (combined read+write)", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer w.Close()

		c := &conn{
			file:       r,
			localAddr:  &Addr{CID: 3, Port: 1024},
			remoteAddr: &Addr{CID: 2, Port: 5000},
		}
		defer c.Close()

		// Set combined deadline in the past — reads should time out.
		err = c.SetDeadline(time.Now().Add(-1 * time.Second))
		Expect(err).NotTo(HaveOccurred())

		buf := make([]byte, 64)
		_, err = c.Read(buf)
		Expect(err).To(HaveOccurred())
		Expect(os.IsTimeout(err)).To(BeTrue())
	})
})

var _ = Describe("NewListener", func() {
	It("should return an error on non-Linux platforms", func() {
		if isLinux() {
			Skip("skipping non-Linux test on Linux")
		}

		_, err := NewListener(1024)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("AF_VSOCK"))
	})
})

var _ = Describe("TransportAvailable", func() {
	It("should return false on non-Linux platforms", func() {
		if isLinux() {
			Skip("skipping non-Linux test on Linux")
		}

		Expect(TransportAvailable()).To(BeFalse())
	})
})

// isLinux returns true when running on Linux (runtime check).
func isLinux() bool {
	_, err := os.Stat("/proc/self")
	return err == nil
}
