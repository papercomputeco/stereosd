// protocol.go defines the JSON message protocol used for communication
// over the vsock channel (host <-> stereosd).
//
// All messages are newline-delimited JSON (one JSON object per line).
// Each message has a "type" field that determines how the payload is interpreted.
package stereosd

import (
	"encoding/json"
	"fmt"
)

// MessageType identifies the kind of control plane message.
type MessageType string

const (
	// -- Host -> Guest (vsock) messages -------------------------------------

	// MsgPing is a health check request from the host.
	MsgPing MessageType = "ping"

	// MsgInjectSecret requests stereosd to write a secret to the tmpfs secret store.
	MsgInjectSecret MessageType = "inject_secret"

	// MsgMount requests stereosd to mount a shared directory.
	MsgMount MessageType = "mount"

	// MsgShutdown requests a graceful shutdown of the StereOS instance.
	MsgShutdown MessageType = "shutdown"

	// -- Guest -> Host (vsock) messages -------------------------------------

	// MsgPong is the response to a ping.
	MsgPong MessageType = "pong"

	// MsgLifecycle reports boot status, readiness, or health.
	MsgLifecycle MessageType = "lifecycle"

	// MsgAck acknowledges a command (success or failure).
	MsgAck MessageType = "ack"

	// -- Health query messages -----------------------------------------------

	// MsgGetHealth requests current health status.
	MsgGetHealth MessageType = "get_health"

	// MsgHealth is the response to a get_health request.
	MsgHealth MessageType = "health"
)

// Envelope is the top-level wire format for all messages.
// The Type field determines how Payload should be decoded.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope creates an envelope with the given type and payload.
// The payload is JSON-encoded. If payload is nil, the Payload field is omitted.
func NewEnvelope(msgType MessageType, payload any) (*Envelope, error) {
	env := &Envelope{Type: msgType}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload for %s: %w", msgType, err)
		}
		env.Payload = data
	}
	return env, nil
}

// DecodePayload unmarshals the envelope's payload into the given target.
func (e *Envelope) DecodePayload(target any) error {
	if e.Payload == nil {
		return fmt.Errorf("envelope %s has no payload", e.Type)
	}
	return json.Unmarshal(e.Payload, target)
}

// -- Payload types ----------------------------------------------------------

// LifecycleState represents the current state of the StereOS instance.
type LifecycleState string

const (
	StateBooting  LifecycleState = "booting"
	StateReady    LifecycleState = "ready"
	StateHealthy  LifecycleState = "healthy"
	StateDegraded LifecycleState = "degraded"
	StateShutdown LifecycleState = "shutdown"
)

// LifecyclePayload is the payload for MsgLifecycle messages.
type LifecyclePayload struct {
	State   LifecycleState `json:"state"`
	Message string         `json:"message,omitempty"`
}

// SecretPayload is the payload for MsgInjectSecret messages.
type SecretPayload struct {
	// Name is the filename under /run/stereos/secrets/ (e.g., "ANTHROPIC_API_KEY").
	Name string `json:"name"`
	// Value is the secret content. Cleared from memory after writing to disk.
	Value string `json:"value"`
	// Mode is the file permission (octal). Defaults to 0600 if zero.
	Mode uint32 `json:"mode,omitempty"`
}

// MountPayload is the payload for MsgMount messages.
type MountPayload struct {
	// Tag is the virtio-fs or 9p tag configured on the host.
	Tag string `json:"tag"`
	// GuestPath is where to mount inside the guest.
	GuestPath string `json:"guest_path"`
	// FSType is the filesystem type: "virtiofs" or "9p".
	FSType string `json:"fs_type"`
	// ReadOnly mounts the filesystem read-only if true.
	ReadOnly bool `json:"read_only,omitempty"`
}

// AckPayload is the payload for MsgAck messages.
type AckPayload struct {
	// ReplyTo is the message type this ack is responding to.
	ReplyTo MessageType `json:"reply_to"`
	// OK is true if the operation succeeded.
	OK bool `json:"ok"`
	// Error is set if OK is false.
	Error string `json:"error,omitempty"`
}

// AgentStatusPayload represents the runtime state of a single agent harness.
// Fields match agentd's API response from GET /v1/agents.
type AgentStatusPayload struct {
	// Name is the agent harness name (e.g., "claude-code", "opencode").
	Name string `json:"name"`
	// Running is true if the agent is currently running.
	Running bool `json:"running"`
	// Session is the tmux session name, if running.
	Session string `json:"session,omitempty"`
	// Restarts is the number of times the agent has been restarted.
	Restarts int `json:"restarts"`
	// Error is set if the agent is in an error state.
	Error string `json:"error,omitempty"`
}

// HealthPayload is the payload for MsgHealth messages.
type HealthPayload struct {
	State  LifecycleState       `json:"state"`
	Agents []AgentStatusPayload `json:"agents,omitempty"`
	Uptime int64                `json:"uptime_seconds"`
}

// ShutdownPayload is the payload for MsgShutdown messages.
type ShutdownPayload struct {
	// Reason describes why shutdown was requested.
	Reason string `json:"reason,omitempty"`
}
