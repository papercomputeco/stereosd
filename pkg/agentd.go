// agentd.go implements an HTTP client for agentd's API.
//
// agentd serves a read-only HTTP API on a Unix domain socket
// (/run/stereos/agentd.sock by default). stereosd polls this API on
// a recurring tick to fetch agent status, replacing the previous
// push-based model where agentd posted status updates to stereosd.
//
// Endpoints consumed:
//   - GET /v1/health  — daemon health and uptime
//   - GET /v1/agents  — list all managed agents and their status
package stereosd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

const (
	// AgentdSocketPath is the default Unix socket for agentd's API.
	AgentdSocketPath = "/run/stereos/agentd.sock"

	// DefaultPollInterval is how often stereosd polls agentd for status.
	DefaultPollInterval = 5 * time.Second
)

// AgentdHealthResponse is returned by GET /v1/health on agentd's API.
type AgentdHealthResponse struct {
	State  string `json:"state"`
	Uptime int64  `json:"uptime_seconds"`
}

// AgentdClient is an HTTP client for agentd's API served over a Unix
// domain socket.
type AgentdClient struct {
	socketPath string
	client     *http.Client
}

// NewAgentdClient creates a new client that connects to agentd over
// the given Unix socket path.
func NewAgentdClient(socketPath string) *AgentdClient {
	return &AgentdClient{
		socketPath: socketPath,
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
			Timeout: 5 * time.Second,
		},
	}
}

// SocketPath returns the socket path this client connects to.
func (c *AgentdClient) SocketPath() string {
	return c.socketPath
}

// Health fetches agentd's health status.
func (c *AgentdClient) Health(ctx context.Context) (*AgentdHealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentd/v1/health", nil)
	if err != nil {
		return nil, fmt.Errorf("agentd client: build health request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentd client: health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentd client: health: unexpected status %d: %s", resp.StatusCode, body)
	}

	var health AgentdHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("agentd client: health: decode: %w", err)
	}
	return &health, nil
}

// Agents fetches the status of all agents from agentd.
func (c *AgentdClient) Agents(ctx context.Context) ([]AgentStatusPayload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentd/v1/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("agentd client: build agents request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentd client: agents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentd client: agents: unexpected status %d: %s", resp.StatusCode, body)
	}

	var agents []AgentStatusPayload
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, fmt.Errorf("agentd client: agents: decode: %w", err)
	}
	return agents, nil
}

// AgentdPoller periodically polls agentd's API and updates the
// lifecycle manager with the latest agent status.
type AgentdPoller struct {
	client    *AgentdClient
	lifecycle *LifecycleManager
	interval  time.Duration
}

// NewAgentdPoller creates a new poller that fetches agent status from
// agentd at the given interval and updates the lifecycle manager.
func NewAgentdPoller(client *AgentdClient, lifecycle *LifecycleManager, interval time.Duration) *AgentdPoller {
	return &AgentdPoller{
		client:    client,
		lifecycle: lifecycle,
		interval:  interval,
	}
}

// Run starts the polling loop. It blocks until the context is cancelled.
// The first poll happens immediately.
func (p *AgentdPoller) Run(ctx context.Context) {
	p.poll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

// poll fetches agent status from agentd and updates the lifecycle manager.
func (p *AgentdPoller) poll(ctx context.Context) {
	agents, err := p.client.Agents(ctx)
	if err != nil {
		// agentd may not be running yet — this is expected during boot.
		log.Printf("agentd poller: %v", err)
		return
	}

	// Replace all agent statuses in the lifecycle manager.
	p.lifecycle.ReplaceAgentStatuses(agents)

	// Transition to healthy if we have running agents and are in the ready state.
	hasRunning := false
	for _, a := range agents {
		if a.Running {
			hasRunning = true
			break
		}
	}

	if hasRunning && p.lifecycle.State() == StateReady {
		p.lifecycle.Transition(StateHealthy, "agents running")
	}
}
