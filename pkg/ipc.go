// ipc.go implements the HTTP API server on the unix socket for
// stereosd <-> agentd (and operator tooling) communication.
//
// The API is a simple JSON-over-HTTP interface on /run/stereos/stereosd.sock:
//
//	GET  /v1/ping                 — health check
//	GET  /v1/health               — full health status
//	POST /v1/secrets              — inject a secret
//	GET  /v1/secrets              — list secrets
//	DELETE /v1/secrets/{name}     — remove a secret
//	POST /v1/mounts              — mount a shared directory
//	GET  /v1/mounts              — list active mounts
//	POST /v1/shutdown             — initiate graceful shutdown
//	POST /v1/agents/status        — report agent status (from agentd)
//	POST /v1/agents/stopped       — confirm agents stopped (from agentd)
//
// This replaces the raw newline-delimited JSON protocol on the IPC socket,
// making it testable with standard HTTP tools (curl, hurl).
package stereosd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// IPCServer handles the HTTP API on the unix socket.
type IPCServer struct {
	socketPath string
	listener   net.Listener
	server     *http.Server
	daemon     *Daemon
}

// NewIPCServer creates a new IPC server at the given socket path.
// The daemon pointer is set later via SetDaemon before Serve is called.
func NewIPCServer(socketPath string) *IPCServer {
	return &IPCServer{
		socketPath: socketPath,
	}
}

// SetDaemon wires the IPC server to the daemon for handling requests.
func (s *IPCServer) SetDaemon(d *Daemon) {
	s.daemon = d
}

// Serve starts the HTTP server on the unix socket.
func (s *IPCServer) Serve(ctx context.Context) error {
	// Remove stale socket file
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("ipc: listen %s: %w", s.socketPath, err)
	}
	s.listener = listener

	if err := os.Chmod(s.socketPath, 0660); err != nil {
		listener.Close()
		return fmt.Errorf("ipc: chmod %s: %w", s.socketPath, err)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.server = &http.Server{
		Handler: mux,
	}

	log.Printf("ipc: HTTP API listening on %s", s.socketPath)

	// Close the server when context is cancelled
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
	}()

	if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("ipc: serve: %w", err)
	}
	return nil
}

// Close shuts down the IPC server and removes the socket file.
func (s *IPCServer) Close() error {
	if s.server != nil {
		s.server.Close()
	}
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
	return nil
}

// IsAgentdConnected returns true. With the HTTP API, agentd connectivity
// is determined per-request rather than by persistent connection.
// For shutdown coordination, we optimistically attempt notification.
func (s *IPCServer) IsAgentdConnected() bool {
	return true
}

// Send sends a message to agentd. In the HTTP model, this is a no-op
// because the shutdown coordinator now uses systemd dependency ordering
// (agentd is stopped before stereosd via Requires=stereosd.service).
func (s *IPCServer) Send(env *Envelope) error {
	log.Printf("ipc: send %s (agentd will be notified via systemd)", env.Type)
	return nil
}

// registerRoutes sets up the HTTP routes.
func (s *IPCServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ping", s.handlePing)
	mux.HandleFunc("GET /v1/health", s.handleGetHealth)
	mux.HandleFunc("POST /v1/secrets", s.handleInjectSecret)
	mux.HandleFunc("GET /v1/secrets", s.handleListSecrets)
	mux.HandleFunc("DELETE /v1/secrets/", s.handleDeleteSecret)
	mux.HandleFunc("POST /v1/mounts", s.handleMount)
	mux.HandleFunc("GET /v1/mounts", s.handleListMounts)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)
	mux.HandleFunc("POST /v1/agents/status", s.handleAgentStatus)
	mux.HandleFunc("POST /v1/agents/stopped", s.handleAgentsStopped)
}

// -- Handlers ---------------------------------------------------------------

func (s *IPCServer) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *IPCServer) handleGetHealth(w http.ResponseWriter, r *http.Request) {
	health := s.daemon.lifecycle.Health()
	writeJSON(w, http.StatusOK, health)
}

func (s *IPCServer) handleInjectSecret(w http.ResponseWriter, r *http.Request) {
	var payload SecretPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}

	if err := s.daemon.secrets.Inject(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "name": payload.Name})
}

func (s *IPCServer) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	names, err := s.daemon.secrets.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list secrets: %v", err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": names})
}

func (s *IPCServer) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/secrets/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "secret name required")
		return
	}

	if err := s.daemon.secrets.Remove(name); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "name": name})
}

func (s *IPCServer) handleMount(w http.ResponseWriter, r *http.Request) {
	var payload MountPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}

	if err := s.daemon.mounts.Mount(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"status":     "ok",
		"tag":        payload.Tag,
		"guest_path": payload.GuestPath,
	})
}

func (s *IPCServer) handleListMounts(w http.ResponseWriter, r *http.Request) {
	mounts := s.daemon.mounts.ActiveMounts()
	writeJSON(w, http.StatusOK, map[string]any{"mounts": mounts})
}

func (s *IPCServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	var payload ShutdownPayload
	json.NewDecoder(r.Body).Decode(&payload)

	go func() {
		ctx := context.Background()
		if err := s.daemon.shutdown.Execute(ctx, &payload); err != nil {
			log.Printf("stereosd: shutdown error: %v", err)
		}
		s.daemon.requestShutdown()
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting_down"})
}

func (s *IPCServer) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	var payload AgentStatusPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}

	s.daemon.lifecycle.UpdateAgentStatus(payload)

	if s.daemon.lifecycle.State() == StateReady {
		s.daemon.lifecycle.Transition(StateHealthy, "agents running")
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *IPCServer) handleAgentsStopped(w http.ResponseWriter, r *http.Request) {
	log.Println("stereosd: agentd confirmed all agents stopped")
	s.daemon.shutdown.NotifyAgentsStopped()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -- JSON response helpers --------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("ipc: error: %s", msg)
	writeJSON(w, status, map[string]string{"error": msg})
}
