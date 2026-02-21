package tcp_test

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/papercomputeco/stereosd/pkg/tcp"
)

func TestTCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "tcp Suite")
}

var _ = Describe("NewListener", func() {
	It("should satisfy the net.Listener interface", func() {
		var _ net.Listener = mustListen()
	})

	It("should create a listener with a valid address", func() {
		ln := mustListen()
		defer ln.Close()

		Expect(ln.Addr()).NotTo(BeNil())
		Expect(ln.Addr().String()).NotTo(BeEmpty())
		Expect(ln.Addr().Network()).To(Equal("tcp"))
	})

	It("should accept TCP connections", func() {
		ln := mustListen()
		defer ln.Close()

		// Connect from a goroutine.
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				return
			}
			conn.Write([]byte("ping"))
			conn.Close()
		}()

		conn, err := ln.Accept()
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("ping"))

		Eventually(done).Should(BeClosed())
	})

	It("should support bidirectional communication", func() {
		ln := mustListen()
		defer ln.Close()

		done := make(chan string, 1)
		go func() {
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				return
			}
			defer conn.Close()

			conn.Write([]byte("request"))

			buf := make([]byte, 64)
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			done <- string(buf[:n])
		}()

		conn, err := ln.Accept()
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("request"))

		_, err = conn.Write([]byte("response"))
		Expect(err).NotTo(HaveOccurred())

		Eventually(done).Should(Receive(Equal("response")))
	})

	It("should return an error after Close", func() {
		ln := mustListen()
		Expect(ln.Close()).To(Succeed())

		_, err := ln.Accept()
		Expect(err).To(HaveOccurred())
	})

	It("should handle multiple sequential connections", func() {
		ln := mustListen()
		defer ln.Close()

		for i := range 3 {
			go func(id int) {
				conn, err := net.Dial("tcp", ln.Addr().String())
				if err != nil {
					return
				}
				conn.Write([]byte("hello"))
				conn.Close()
			}(i)

			conn, err := ln.Accept()
			Expect(err).NotTo(HaveOccurred())

			buf := make([]byte, 64)
			n, err := conn.Read(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(buf[:n])).To(Equal("hello"))
			conn.Close()
		}
	})
})

// mustListen creates a TCP listener on an ephemeral port. It fails the
// test if the listener cannot be created.
func mustListen() net.Listener {
	ln, err := tcp.NewListener(0)
	Expect(err).NotTo(HaveOccurred())
	return ln
}
