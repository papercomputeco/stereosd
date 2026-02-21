// server.go implements the control plane message server.
//
// The server accepts connections on a [net.Listener] (typically AF_VSOCK or
// TCP) and processes newline-delimited JSON [Envelope] messages. Each
// connection is handled in its own goroutine.
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

// Handler processes messages received from the host and returns a response
// envelope (or nil for no response).
type Handler interface {
	HandleMessage(ctx context.Context, env *Envelope) (*Envelope, error)
}

// Server handles incoming host connections over a [net.Listener].
type Server struct {
	listener net.Listener
	handler  Handler
	wg       sync.WaitGroup
}

// NewServer creates a new control plane server with the given listener and
// handler.
func NewServer(listener net.Listener, handler Handler) *Server {
	return &Server{
		listener: listener,
		handler:  handler,
	}
}

// Serve accepts connections until the context is cancelled.
// It blocks until all active connections have finished.
func (s *Server) Serve(ctx context.Context) error {
	log.Printf("server: listening on %s", s.listener.Addr())

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
				return fmt.Errorf("accept: %w", err)
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

// handleConnection processes messages from a single connection.
// Each connection can carry multiple newline-delimited JSON messages.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr()
	log.Printf("server: new connection from %s", remote)

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
			log.Printf("server: malformed message from %s: %v", remote, err)
			// Send error ack
			ackEnv, _ := NewEnvelope(MsgAck, &AckPayload{
				OK:    false,
				Error: fmt.Sprintf("malformed JSON: %v", err),
			})
			encoder.Encode(ackEnv)
			continue
		}

		resp, err := s.handler.HandleMessage(ctx, &env)
		if err != nil {
			log.Printf("server: error handling %s from %s: %v", env.Type, remote, err)
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
				log.Printf("server: error writing response to %s: %v", remote, err)
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("server: read error from %s: %v", remote, err)
	}

	log.Printf("server: connection from %s closed", remote)
}
