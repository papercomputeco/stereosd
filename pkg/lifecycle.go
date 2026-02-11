// lifecycle.go implements lifecycle state tracking and reporting.
//
// stereosd tracks the current state of the StereOS instance and reports it
// to the host over vsock. States progress through:
//
//	booting -> ready -> healthy (or degraded) -> shutdown
package stereosd

import (
	"log"
	"sync"
	"time"
)

// LifecycleManager tracks the lifecycle state of the StereOS instance.
type LifecycleManager struct {
	mu        sync.RWMutex
	state     LifecycleState
	message   string
	bootTime  time.Time
	agents    []AgentStatusPayload
	vsockSend func(*Envelope) // callback to send lifecycle updates over vsock
}

// NewLifecycleManager creates a new lifecycle manager.
// The vsockSend callback is used to push lifecycle state changes to the host.
// It may be nil if vsock is not yet available (will be set later via SetVsockSender).
func NewLifecycleManager() *LifecycleManager {
	return &LifecycleManager{
		state:    StateBooting,
		bootTime: time.Now(),
	}
}

// SetVsockSender sets the callback used to send lifecycle updates to the host.
func (lm *LifecycleManager) SetVsockSender(send func(*Envelope)) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.vsockSend = send
}

// Transition moves the lifecycle to a new state and notifies the host.
func (lm *LifecycleManager) Transition(state LifecycleState, message string) {
	lm.mu.Lock()
	prev := lm.state
	lm.state = state
	lm.message = message
	send := lm.vsockSend
	lm.mu.Unlock()

	log.Printf("lifecycle: %s -> %s: %s", prev, state, message)

	if send != nil {
		env, err := NewEnvelope(MsgLifecycle, &LifecyclePayload{
			State:   state,
			Message: message,
		})
		if err != nil {
			log.Printf("lifecycle: failed to create envelope: %v", err)
			return
		}
		send(env)
	}
}

// State returns the current lifecycle state.
func (lm *LifecycleManager) State() LifecycleState {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.state
}

// Uptime returns the duration since the daemon started.
func (lm *LifecycleManager) Uptime() time.Duration {
	return time.Since(lm.bootTime)
}

// UpdateAgentStatus records the status of an agent harness.
// This is called when agentd sends agent_status messages.
func (lm *LifecycleManager) UpdateAgentStatus(status AgentStatusPayload) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Update or append
	for i, existing := range lm.agents {
		if existing.Name == status.Name {
			lm.agents[i] = status
			return
		}
	}
	lm.agents = append(lm.agents, status)
}

// Health returns the current health payload for reporting.
func (lm *LifecycleManager) Health() HealthPayload {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	agents := make([]AgentStatusPayload, len(lm.agents))
	copy(agents, lm.agents)

	return HealthPayload{
		State:  lm.state,
		Agents: agents,
		Uptime: int64(time.Since(lm.bootTime).Seconds()),
	}
}
