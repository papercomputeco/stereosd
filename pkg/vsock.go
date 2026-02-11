// vsock.go implements the virtio-vsock listener for host <-> guest communication.
//
// The vsock transport (AF_VSOCK) is used instead of network sockets because:
//   - It works even when network.mode = "none" (air-gapped)
//   - It avoids port conflicts with guest services
//   - It provides a clean separation between control plane and data plane
//
// The host connects to CID:3 port 1024. Each connection is handled in its
// own goroutine, reading newline-delimited JSON messages.
package stereosd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
)

// VsockListener is an interface for the vsock listening socket.
// This allows testing with regular TCP/Unix sockets while using
// real vsock in production.
type VsockListener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() net.Addr
}

// VsockServer handles incoming host connections over vsock.
type VsockServer struct {
	listener VsockListener
	handler  VsockHandler
	wg       sync.WaitGroup
}

// VsockHandler processes messages received from the host over vsock
// and returns a response envelope (or nil for no response).
type VsockHandler interface {
	HandleVsockMessage(ctx context.Context, env *Envelope) (*Envelope, error)
}

// NewVsockServer creates a new vsock server with the given listener and handler.
func NewVsockServer(listener VsockListener, handler VsockHandler) *VsockServer {
	return &VsockServer{
		listener: listener,
		handler:  handler,
	}
}

// Serve accepts connections until the context is cancelled.
// It blocks until all active connections have finished.
func (s *VsockServer) Serve(ctx context.Context) error {
	log.Printf("vsock: listening on %s", s.listener.Addr())

	// Close the listener when context is cancelled to unblock Accept()
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Check if we're shutting down
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
				return fmt.Errorf("vsock accept: %w", err)
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// handleConnection processes messages from a single vsock connection.
// Each connection can carry multiple newline-delimited JSON messages.
func (s *VsockServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr()
	log.Printf("vsock: new connection from %s", remote)

	scanner := bufio.NewScanner(conn)

	// Allow messages up to 1MB
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			log.Printf("vsock: malformed message from %s: %v", remote, err)
			// Send error ack
			ackEnv, _ := NewEnvelope(MsgAck, &AckPayload{
				OK:    false,
				Error: fmt.Sprintf("malformed JSON: %v", err),
			})
			encoder.Encode(ackEnv)
			continue
		}

		resp, err := s.handler.HandleVsockMessage(ctx, &env)
		if err != nil {
			log.Printf("vsock: error handling %s from %s: %v", env.Type, remote, err)
			ackEnv, _ := NewEnvelope(MsgAck, &AckPayload{
				ReplyTo: env.Type,
				OK:      false,
				Error:   err.Error(),
			})
			encoder.Encode(ackEnv)
			continue
		}

		if resp != nil {
			if err := encoder.Encode(resp); err != nil {
				log.Printf("vsock: error writing response to %s: %v", remote, err)
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("vsock: read error from %s: %v", remote, err)
	}

	log.Printf("vsock: connection from %s closed", remote)
}
