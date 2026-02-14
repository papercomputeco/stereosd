package stereosd_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/papercomputeco/stereosd/pkg"
)

func TestStereosd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "stereosd Suite")
}

// -- Test helpers -----------------------------------------------------------

// mockCommander records commands instead of executing them.
type mockCommander struct {
	mu       sync.Mutex
	commands [][]string
	failOn   map[string]error
}

func newMockCommander() *mockCommander {
	return &mockCommander{
		failOn: make(map[string]error),
	}
}

func (m *mockCommander) Run(name string, args ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := append([]string{name}, args...)
	m.commands = append(m.commands, cmd)
	if err, ok := m.failOn[name]; ok {
		return err
	}
	return nil
}

func (m *mockCommander) Output(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := append([]string{name}, args...)
	m.commands = append(m.commands, cmd)
	if err, ok := m.failOn[name]; ok {
		return nil, err
	}
	return []byte("ok"), nil
}

func (m *mockCommander) hasCommand(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cmd := range m.commands {
		if cmd[0] == name {
			return true
		}
	}
	return false
}

func (m *mockCommander) getCommands() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]string, len(m.commands))
	copy(result, m.commands)
	return result
}

// testListenerFactory creates a TCP listener as a vsock substitute for tests.
func testListenerFactory(port uint32) (stereosd.VsockListener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &testVsockListener{listener: listener}, nil
}

type testVsockListener struct {
	listener net.Listener
}

func (t *testVsockListener) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *testVsockListener) Close() error              { return t.listener.Close() }
func (t *testVsockListener) Addr() net.Addr            { return t.listener.Addr() }

// sendVsockMessage connects to a TCP address and sends a JSON message,
// returning the response envelope.
func sendVsockMessage(addr string, env *stereosd.Envelope) (*stereosd.Envelope, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(env); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		return nil, fmt.Errorf("no response")
	}

	var resp stereosd.Envelope
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

// ipcHTTPClient returns an *http.Client that dials the given unix socket.
func ipcHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", socketPath, 2*time.Second)
			},
		},
		Timeout: 5 * time.Second,
	}
}

// ipcGet performs an HTTP GET against the IPC unix socket and returns the
// decoded JSON body.
func ipcGet(socketPath, path string) (map[string]any, int, error) {
	client := ipcHTTPClient(socketPath)
	resp, err := client.Get("http://localhost" + path)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("unmarshal: %w (body: %s)", err, body)
	}
	return result, resp.StatusCode, nil
}

// ipcPost performs an HTTP POST with a JSON body against the IPC unix socket
// and returns the decoded JSON response.
func ipcPost(socketPath, path string, payload any) (map[string]any, int, error) {
	client := ipcHTTPClient(socketPath)
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(data)
	}
	resp, err := client.Post("http://localhost"+path, "application/json", body)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("unmarshal: %w (body: %s)", err, respBody)
	}
	return result, resp.StatusCode, nil
}

// ============================================================================
// Protocol tests
// ============================================================================

var _ = Describe("Protocol", func() {
	Describe("Envelope", func() {
		It("should create an envelope with a payload", func() {
			env, err := stereosd.NewEnvelope(stereosd.MsgPing, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(env.Type).To(Equal(stereosd.MsgPing))
			Expect(env.Payload).To(BeNil())
		})

		It("should create an envelope with a typed payload", func() {
			payload := &stereosd.LifecyclePayload{
				State:   stereosd.StateReady,
				Message: "all systems go",
			}
			env, err := stereosd.NewEnvelope(stereosd.MsgLifecycle, payload)
			Expect(err).NotTo(HaveOccurred())
			Expect(env.Type).To(Equal(stereosd.MsgLifecycle))
			Expect(env.Payload).NotTo(BeNil())
		})

		It("should decode a payload from an envelope", func() {
			payload := &stereosd.SecretPayload{
				Name:  "API_KEY",
				Value: "sk-test-12345",
				Mode:  0600,
			}
			env, err := stereosd.NewEnvelope(stereosd.MsgInjectSecret, payload)
			Expect(err).NotTo(HaveOccurred())

			var decoded stereosd.SecretPayload
			Expect(env.DecodePayload(&decoded)).To(Succeed())
			Expect(decoded.Name).To(Equal("API_KEY"))
			Expect(decoded.Value).To(Equal("sk-test-12345"))
			Expect(decoded.Mode).To(Equal(uint32(0600)))
		})

		It("should return an error when decoding a nil payload", func() {
			env, _ := stereosd.NewEnvelope(stereosd.MsgPing, nil)
			var target struct{}
			Expect(env.DecodePayload(&target)).To(HaveOccurred())
		})

		It("should roundtrip through JSON", func() {
			original, _ := stereosd.NewEnvelope(stereosd.MsgAck, &stereosd.AckPayload{
				ReplyTo: stereosd.MsgInjectSecret,
				OK:      true,
			})
			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var restored stereosd.Envelope
			Expect(json.Unmarshal(data, &restored)).To(Succeed())
			Expect(restored.Type).To(Equal(stereosd.MsgAck))

			var ack stereosd.AckPayload
			Expect(restored.DecodePayload(&ack)).To(Succeed())
			Expect(ack.OK).To(BeTrue())
			Expect(ack.ReplyTo).To(Equal(stereosd.MsgInjectSecret))
		})
	})

	Describe("Constants", func() {
		It("should use the correct vsock port", func() {
			Expect(stereosd.VsockPort).To(BeEquivalentTo(1024))
		})

		It("should use tmpfs-backed secret directory", func() {
			Expect(stereosd.SecretDir).To(Equal("/run/stereos/secrets"))
		})

		It("should use the correct unix socket path", func() {
			Expect(stereosd.SocketPath).To(Equal("/run/stereos/stereosd.sock"))
		})
	})
})

// ============================================================================
// Lifecycle tests
// ============================================================================

var _ = Describe("LifecycleManager", func() {
	var lm *stereosd.LifecycleManager

	BeforeEach(func() {
		lm = stereosd.NewLifecycleManager()
	})

	It("should start in booting state", func() {
		Expect(lm.State()).To(Equal(stereosd.StateBooting))
	})

	It("should transition states", func() {
		lm.Transition(stereosd.StateReady, "all systems go")
		Expect(lm.State()).To(Equal(stereosd.StateReady))

		lm.Transition(stereosd.StateHealthy, "agents running")
		Expect(lm.State()).To(Equal(stereosd.StateHealthy))
	})

	It("should track uptime", func() {
		time.Sleep(10 * time.Millisecond)
		Expect(lm.Uptime()).To(BeNumerically(">", 0))
	})

	It("should track agent status", func() {
		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "opencode",
			Running: true,
			Session: "opencode-main",
		})
		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "claude-code",
			Running: true,
			Session: "claude-main",
		})

		health := lm.Health()
		Expect(health.Agents).To(HaveLen(2))
		Expect(health.Agents[0].Name).To(Equal("opencode"))
		Expect(health.Agents[1].Name).To(Equal("claude-code"))
	})

	It("should update existing agent status", func() {
		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "opencode",
			Running: true,
		})
		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "opencode",
			Running: false,
			Error:   "crashed",
		})

		health := lm.Health()
		Expect(health.Agents).To(HaveLen(1))
		Expect(health.Agents[0].Running).To(BeFalse())
		Expect(health.Agents[0].Error).To(Equal("crashed"))
	})

	It("should report health with uptime", func() {
		lm.Transition(stereosd.StateReady, "ready")
		health := lm.Health()
		Expect(health.State).To(Equal(stereosd.StateReady))
		Expect(health.Uptime).To(BeNumerically(">=", 0))
	})

	It("should notify via vsock sender when transitioning", func() {
		var received *stereosd.Envelope
		lm.SetVsockSender(func(env *stereosd.Envelope) {
			received = env
		})

		lm.Transition(stereosd.StateReady, "ready")
		Expect(received).NotTo(BeNil())
		Expect(received.Type).To(Equal(stereosd.MsgLifecycle))
	})
})

// ============================================================================
// Secret injection tests
// ============================================================================

var _ = Describe("SecretManager", func() {
	var (
		sm        *stereosd.SecretManager
		secretDir string
	)

	BeforeEach(func() {
		var err error
		secretDir, err = os.MkdirTemp("", "stereos-secrets-test-*")
		Expect(err).NotTo(HaveOccurred())
		sm = stereosd.NewSecretManager(secretDir)
	})

	AfterEach(func() {
		os.RemoveAll(secretDir)
	})

	It("should inject a secret to the filesystem", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "ANTHROPIC_API_KEY",
			Value: "sk-ant-test-12345",
		})
		Expect(err).NotTo(HaveOccurred())

		content, err := os.ReadFile(filepath.Join(secretDir, "ANTHROPIC_API_KEY"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("sk-ant-test-12345"))
	})

	It("should set default permissions to 0600", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "SECRET",
			Value: "value",
		})
		Expect(err).NotTo(HaveOccurred())

		info, err := os.Stat(filepath.Join(secretDir, "SECRET"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})

	It("should set custom permissions", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "PUBLIC_KEY",
			Value: "value",
			Mode:  0644,
		})
		Expect(err).NotTo(HaveOccurred())

		info, err := os.Stat(filepath.Join(secretDir, "PUBLIC_KEY"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0644)))
	})

	It("should clear the secret value from the payload after injection", func() {
		payload := &stereosd.SecretPayload{
			Name:  "KEY",
			Value: "sensitive-data",
		}
		Expect(sm.Inject(payload)).To(Succeed())
		Expect(payload.Value).To(BeEmpty())
	})

	It("should reject empty names", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "",
			Value: "value",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty"))
	})

	It("should reject path traversal attempts", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "../etc/passwd",
			Value: "evil",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid secret name"))
	})

	It("should reject names with slashes", func() {
		err := sm.Inject(&stereosd.SecretPayload{
			Name:  "foo/bar",
			Value: "value",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid secret name"))
	})

	It("should overwrite existing secrets", func() {
		Expect(sm.Inject(&stereosd.SecretPayload{
			Name:  "KEY",
			Value: "version1",
		})).To(Succeed())

		Expect(sm.Inject(&stereosd.SecretPayload{
			Name:  "KEY",
			Value: "version2",
		})).To(Succeed())

		content, err := os.ReadFile(filepath.Join(secretDir, "KEY"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("version2"))
	})

	It("should list injected secrets", func() {
		Expect(sm.Inject(&stereosd.SecretPayload{Name: "A", Value: "1"})).To(Succeed())
		Expect(sm.Inject(&stereosd.SecretPayload{Name: "B", Value: "2"})).To(Succeed())

		names, err := sm.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(ConsistOf("A", "B"))
	})

	It("should remove secrets", func() {
		Expect(sm.Inject(&stereosd.SecretPayload{Name: "KEY", Value: "val"})).To(Succeed())
		Expect(sm.Remove("KEY")).To(Succeed())

		_, err := os.Stat(filepath.Join(secretDir, "KEY"))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("should not error when removing non-existent secrets", func() {
		Expect(sm.Remove("nonexistent")).To(Succeed())
	})
})

// ============================================================================
// Mount manager tests
// ============================================================================

var _ = Describe("MountManager", func() {
	var (
		mm  *stereosd.MountManager
		cmd *mockCommander
	)

	BeforeEach(func() {
		cmd = newMockCommander()
		mm = stereosd.NewMountManager(cmd)
	})

	It("should mount a virtiofs share", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-mount-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		mountPath := filepath.Join(tmpDir, "workspace")
		err = mm.Mount(&stereosd.MountPayload{
			Tag:       "workspace0",
			GuestPath: mountPath,
			FSType:    "virtiofs",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.hasCommand("mount")).To(BeTrue())
	})

	It("should mount a 9p share with correct options", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-mount-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		mountPath := filepath.Join(tmpDir, "data")
		err = mm.Mount(&stereosd.MountPayload{
			Tag:       "data0",
			GuestPath: mountPath,
			FSType:    "9p",
		})
		Expect(err).NotTo(HaveOccurred())

		// Verify 9p-specific mount options were used
		found := false
		for _, c := range cmd.commands {
			if c[0] == "mount" {
				for _, arg := range c {
					if arg == "trans=virtio,version=9p2000.L" {
						found = true
					}
				}
			}
		}
		Expect(found).To(BeTrue(), "should include 9p transport options")
	})

	It("should reject empty tag", func() {
		err := mm.Mount(&stereosd.MountPayload{
			Tag:       "",
			GuestPath: "/workspace",
			FSType:    "virtiofs",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tag"))
	})

	It("should reject empty guest_path", func() {
		err := mm.Mount(&stereosd.MountPayload{
			Tag:       "test",
			GuestPath: "",
			FSType:    "virtiofs",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("guest_path"))
	})

	It("should reject unsupported filesystem types", func() {
		err := mm.Mount(&stereosd.MountPayload{
			Tag:       "test",
			GuestPath: "/mnt/test",
			FSType:    "ext4",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported"))
	})

	It("should reject relative guest paths", func() {
		err := mm.Mount(&stereosd.MountPayload{
			Tag:       "test",
			GuestPath: "relative/path",
			FSType:    "virtiofs",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("absolute"))
	})

	It("should reject mounts over system directories", func() {
		systemDirs := []string{"/", "/nix", "/etc", "/bin", "/boot", "/dev", "/proc", "/sys", "/run"}
		for _, dir := range systemDirs {
			err := mm.Mount(&stereosd.MountPayload{
				Tag:       "test",
				GuestPath: dir,
				FSType:    "virtiofs",
			})
			Expect(err).To(HaveOccurred(), "should reject mount at %s", dir)
			Expect(err.Error()).To(ContainSubstring("system directory"))
		}
	})

	It("should reject mounts under system directories", func() {
		err := mm.Mount(&stereosd.MountPayload{
			Tag:       "test",
			GuestPath: "/nix/store/evil",
			FSType:    "virtiofs",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("system directory"))
	})

	It("should track active mounts", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-mount-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		mount1 := filepath.Join(tmpDir, "a")
		mount2 := filepath.Join(tmpDir, "b")

		Expect(mm.Mount(&stereosd.MountPayload{
			Tag: "a", GuestPath: mount1, FSType: "virtiofs",
		})).To(Succeed())
		Expect(mm.Mount(&stereosd.MountPayload{
			Tag: "b", GuestPath: mount2, FSType: "virtiofs",
		})).To(Succeed())

		mounts := mm.ActiveMounts()
		Expect(mounts).To(HaveLen(2))
	})

	It("should unmount all in reverse order", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-mount-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		mount1 := filepath.Join(tmpDir, "a")
		mount2 := filepath.Join(tmpDir, "b")

		Expect(mm.Mount(&stereosd.MountPayload{
			Tag: "first", GuestPath: mount1, FSType: "virtiofs",
		})).To(Succeed())
		Expect(mm.Mount(&stereosd.MountPayload{
			Tag: "second", GuestPath: mount2, FSType: "virtiofs",
		})).To(Succeed())

		mm.UnmountAll()

		// Verify umount commands were issued
		umountCmds := []string{}
		for _, c := range cmd.commands {
			if c[0] == "umount" {
				umountCmds = append(umountCmds, c[1])
			}
		}
		Expect(umountCmds).To(HaveLen(2))
		// Reverse order: second mount unmounted first
		Expect(umountCmds[0]).To(Equal(mount2))
		Expect(umountCmds[1]).To(Equal(mount1))

		// Active mounts should be empty
		Expect(mm.ActiveMounts()).To(BeEmpty())
	})

	It("should support read-only mounts", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-mount-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		mountPath := filepath.Join(tmpDir, "ro")
		err = mm.Mount(&stereosd.MountPayload{
			Tag:       "rodata",
			GuestPath: mountPath,
			FSType:    "virtiofs",
			ReadOnly:  true,
		})
		Expect(err).NotTo(HaveOccurred())

		// Verify "ro" option was used
		found := false
		for _, c := range cmd.commands {
			if c[0] == "mount" {
				for _, arg := range c {
					if arg == "ro" {
						found = true
					}
				}
			}
		}
		Expect(found).To(BeTrue(), "should include ro option")
	})
})

// ============================================================================
// Runtime directory tests
// ============================================================================

var _ = Describe("RuntimeDirs", func() {
	It("should create runtime directories with correct permissions", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-dirs-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		dirs := stereosd.RuntimeDirs{
			Base:    filepath.Join(tmpDir, "stereos"),
			Secrets: filepath.Join(tmpDir, "stereos", "secrets"),
		}

		Expect(stereosd.EnsureRuntimeDirs(dirs)).To(Succeed())

		info, err := os.Stat(dirs.Base)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0755)))

		info, err = os.Stat(dirs.Secrets)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0700)))
	})

	It("should be idempotent", func() {
		tmpDir, err := os.MkdirTemp("", "stereos-dirs-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		dirs := stereosd.RuntimeDirs{
			Base:    filepath.Join(tmpDir, "stereos"),
			Secrets: filepath.Join(tmpDir, "stereos", "secrets"),
		}

		Expect(stereosd.EnsureRuntimeDirs(dirs)).To(Succeed())
		Expect(stereosd.EnsureRuntimeDirs(dirs)).To(Succeed()) // second call
	})

	It("should return correct default paths", func() {
		dirs := stereosd.DefaultRuntimeDirs()
		Expect(dirs.Base).To(Equal("/run/stereos"))
		Expect(dirs.Secrets).To(Equal("/run/stereos/secrets"))
	})
})

// ============================================================================
// Daemon integration tests
// ============================================================================

var _ = Describe("Daemon", func() {
	var (
		daemon      *stereosd.Daemon
		tmpDir      string
		cmd         *mockCommander
		vsockAddrCh chan string
		ipcSocket   string
	)

	getVsockAddr := func() string {
		select {
		case addr := <-vsockAddrCh:
			// Put it back so subsequent calls still work
			vsockAddrCh <- addr
			return addr
		default:
			return ""
		}
	}

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "stereos-daemon-test-*")
		Expect(err).NotTo(HaveOccurred())

		cmd = newMockCommander()
		ipcSocket = filepath.Join(tmpDir, "stereosd.sock")
		vsockAddrCh = make(chan string, 1)

		// Track the vsock listener address
		var vsockListener net.Listener

		config := stereosd.Config{
			VsockPort:  0, // not used directly; factory overrides
			SocketPath: ipcSocket,
			RuntimeDirs: stereosd.RuntimeDirs{
				Base:    filepath.Join(tmpDir, "stereos"),
				Secrets: filepath.Join(tmpDir, "stereos", "secrets"),
			},
			ListenerFactory: func(port uint32) (stereosd.VsockListener, error) {
				var err error
				vsockListener, err = net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					return nil, err
				}
				vsockAddrCh <- vsockListener.Addr().String()
				return &testVsockListener{listener: vsockListener}, nil
			},
			Commander: cmd,
		}

		daemon = stereosd.NewDaemonWithConfig(config)
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("should create a daemon instance", func() {
		Expect(daemon).NotTo(BeNil())
	})

	It("should start and accept a vsock ping", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- daemon.Run(ctx)
		}()

		// Wait for the daemon to start listening
		Eventually(func() bool {
			addr := getVsockAddr()
			if addr == "" {
				return false
			}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		// Send ping
		env, err := stereosd.NewEnvelope(stereosd.MsgPing, nil)
		Expect(err).NotTo(HaveOccurred())

		resp, err := sendVsockMessage(getVsockAddr(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Type).To(Equal(stereosd.MsgPong))

		cancel()
	})

	It("should inject secrets via vsock", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		// Wait for the daemon to start
		Eventually(func() bool {
			addr := getVsockAddr()
			if addr == "" {
				return false
			}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		// Inject a secret
		env, err := stereosd.NewEnvelope(stereosd.MsgInjectSecret, &stereosd.SecretPayload{
			Name:  "ANTHROPIC_API_KEY",
			Value: "sk-ant-test-12345",
		})
		Expect(err).NotTo(HaveOccurred())

		resp, err := sendVsockMessage(getVsockAddr(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Type).To(Equal(stereosd.MsgAck))

		var ack stereosd.AckPayload
		Expect(resp.DecodePayload(&ack)).To(Succeed())
		Expect(ack.OK).To(BeTrue())

		// Verify the secret was written
		secretPath := filepath.Join(tmpDir, "stereos", "secrets", "ANTHROPIC_API_KEY")
		content, err := os.ReadFile(secretPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("sk-ant-test-12345"))

		cancel()
	})

	It("should handle mount requests via vsock", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		Eventually(func() bool {
			addr := getVsockAddr()
			if addr == "" {
				return false
			}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		mountPath := filepath.Join(tmpDir, "workspace")
		env, err := stereosd.NewEnvelope(stereosd.MsgMount, &stereosd.MountPayload{
			Tag:       "workspace0",
			GuestPath: mountPath,
			FSType:    "virtiofs",
		})
		Expect(err).NotTo(HaveOccurred())

		resp, err := sendVsockMessage(getVsockAddr(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Type).To(Equal(stereosd.MsgAck))

		var ack stereosd.AckPayload
		Expect(resp.DecodePayload(&ack)).To(Succeed())
		Expect(ack.OK).To(BeTrue())

		// Verify mount command was called
		Expect(cmd.hasCommand("mount")).To(BeTrue())

		cancel()
	})

	It("should accept IPC connections from agentd", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		// Wait for the IPC socket to be available
		Eventually(func() bool {
			_, err := os.Stat(ipcSocket)
			return err == nil
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		// Send a health request via HTTP API
		result, status, err := ipcGet(ipcSocket, "/v1/health")
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))
		Expect(result["state"]).To(Equal(string(stereosd.StateReady)))

		cancel()
	})

	It("should serve agent statuses via GET /v1/agents", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		// Wait for the IPC socket to be available
		Eventually(func() bool {
			_, err := os.Stat(ipcSocket)
			return err == nil
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		// Manually update lifecycle with agent statuses (simulating what the poller does)
		daemon.Lifecycle().ReplaceAgentStatuses([]stereosd.AgentStatusPayload{
			{Name: "opencode", Running: true, Session: "opencode-main", Restarts: 0},
		})

		// Fetch agents via the HTTP API
		result, status, err := ipcGet(ipcSocket, "/v1/agents")
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))

		agents, ok := result["agents"].([]any)
		Expect(ok).To(BeTrue())
		Expect(agents).To(HaveLen(1))

		agent := agents[0].(map[string]any)
		Expect(agent["name"]).To(Equal("opencode"))
		Expect(agent["running"]).To(BeTrue())
		Expect(agent["session"]).To(Equal("opencode-main"))

		cancel()
	})

	It("should handle shutdown requests via vsock", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		Eventually(func() bool {
			addr := getVsockAddr()
			if addr == "" {
				return false
			}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		env, err := stereosd.NewEnvelope(stereosd.MsgShutdown, &stereosd.ShutdownPayload{
			Reason: "test shutdown",
		})
		Expect(err).NotTo(HaveOccurred())

		resp, err := sendVsockMessage(getVsockAddr(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Type).To(Equal(stereosd.MsgAck))

		// Verify shutdown sequence was initiated
		Eventually(func() bool {
			return cmd.hasCommand("sync")
		}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())

		cancel()
	})

	It("should create runtime directories on startup", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		// Wait for daemon to start
		Eventually(func() bool {
			_, err := os.Stat(filepath.Join(tmpDir, "stereos"))
			return err == nil
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		info, err := os.Stat(filepath.Join(tmpDir, "stereos"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0755)))

		info, err = os.Stat(filepath.Join(tmpDir, "stereos", "secrets"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0700)))

		cancel()
	})

	It("should reject unknown vsock message types", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go daemon.Run(ctx)

		Eventually(func() bool {
			addr := getVsockAddr()
			if addr == "" {
				return false
			}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		}, 2*time.Second, 50*time.Millisecond).Should(BeTrue())

		env := &stereosd.Envelope{Type: "unknown_type"}
		resp, err := sendVsockMessage(getVsockAddr(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Type).To(Equal(stereosd.MsgAck))

		var ack stereosd.AckPayload
		Expect(resp.DecodePayload(&ack)).To(Succeed())
		Expect(ack.OK).To(BeFalse())
		Expect(ack.Error).To(ContainSubstring("unknown"))

		cancel()
	})
})

// ============================================================================
// VsockServer tests (unit)
// ============================================================================

var _ = Describe("VsockServer", func() {
	It("should handle malformed JSON gracefully", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())

		handler := &mockVsockHandler{}
		server := stereosd.NewVsockServer(
			&testVsockListener{listener: listener},
			handler,
		)

		go server.Serve(ctx)

		// Send malformed JSON
		conn, err := net.Dial("tcp", listener.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		fmt.Fprintln(conn, "this is not json{{{")

		// Should get an error ack back
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var resp stereosd.Envelope
			Expect(json.Unmarshal(scanner.Bytes(), &resp)).To(Succeed())
			Expect(resp.Type).To(Equal(stereosd.MsgAck))
		}

		cancel()
	})
})

type mockVsockHandler struct{}

func (m *mockVsockHandler) HandleVsockMessage(ctx context.Context, env *stereosd.Envelope) (*stereosd.Envelope, error) {
	return stereosd.NewEnvelope(stereosd.MsgPong, nil)
}

// ============================================================================
// AgentdClient tests
// ============================================================================

var _ = Describe("AgentdClient", func() {
	var (
		agentdSocket string
		agentdServer *http.Server
		agentdLn     net.Listener
		tmpDir       string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "stereos-agentd-client-test-*")
		Expect(err).NotTo(HaveOccurred())

		agentdSocket = filepath.Join(tmpDir, "agentd.sock")
	})

	AfterEach(func() {
		if agentdServer != nil {
			agentdServer.Close()
		}
		if agentdLn != nil {
			agentdLn.Close()
		}
		os.RemoveAll(tmpDir)
	})

	startAgentdMock := func(healthHandler, agentsHandler http.HandlerFunc) {
		mux := http.NewServeMux()
		if healthHandler != nil {
			mux.HandleFunc("GET /v1/health", healthHandler)
		}
		if agentsHandler != nil {
			mux.HandleFunc("GET /v1/agents", agentsHandler)
		}

		var err error
		agentdLn, err = net.Listen("unix", agentdSocket)
		Expect(err).NotTo(HaveOccurred())

		agentdServer = &http.Server{Handler: mux}
		go agentdServer.Serve(agentdLn)
	}

	It("should fetch health from agentd", func() {
		startAgentdMock(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"state":          "running",
				"uptime_seconds": 42,
			})
		}, nil)

		client := stereosd.NewAgentdClient(agentdSocket)
		health, err := client.Health(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(health.State).To(Equal("running"))
		Expect(health.Uptime).To(Equal(int64(42)))
	})

	It("should fetch agents from agentd", func() {
		startAgentdMock(nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]stereosd.AgentStatusPayload{
				{Name: "claude-code", Running: true, Session: "claude-main", Restarts: 2},
				{Name: "opencode", Running: false, Error: "crashed", Restarts: 1},
			})
		})

		client := stereosd.NewAgentdClient(agentdSocket)
		agents, err := client.Agents(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(agents).To(HaveLen(2))
		Expect(agents[0].Name).To(Equal("claude-code"))
		Expect(agents[0].Running).To(BeTrue())
		Expect(agents[0].Restarts).To(Equal(2))
		Expect(agents[1].Name).To(Equal("opencode"))
		Expect(agents[1].Running).To(BeFalse())
		Expect(agents[1].Error).To(Equal("crashed"))
	})

	It("should return an error when agentd is not reachable", func() {
		// No mock server started — socket does not exist
		client := stereosd.NewAgentdClient(filepath.Join(tmpDir, "nonexistent.sock"))
		_, err := client.Agents(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("should return the socket path", func() {
		client := stereosd.NewAgentdClient(agentdSocket)
		Expect(client.SocketPath()).To(Equal(agentdSocket))
	})
})

// ============================================================================
// AgentdPoller tests
// ============================================================================

var _ = Describe("AgentdPoller", func() {
	var (
		agentdSocket string
		agentdServer *http.Server
		agentdLn     net.Listener
		tmpDir       string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "stereos-poller-test-*")
		Expect(err).NotTo(HaveOccurred())

		agentdSocket = filepath.Join(tmpDir, "agentd.sock")
	})

	AfterEach(func() {
		if agentdServer != nil {
			agentdServer.Close()
		}
		if agentdLn != nil {
			agentdLn.Close()
		}
		os.RemoveAll(tmpDir)
	})

	It("should poll agentd and update lifecycle agent statuses", func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/agents", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]stereosd.AgentStatusPayload{
				{Name: "opencode", Running: true, Session: "opencode-main"},
			})
		})

		var err error
		agentdLn, err = net.Listen("unix", agentdSocket)
		Expect(err).NotTo(HaveOccurred())
		agentdServer = &http.Server{Handler: mux}
		go agentdServer.Serve(agentdLn)

		lifecycle := stereosd.NewLifecycleManager()
		lifecycle.Transition(stereosd.StateReady, "ready")

		client := stereosd.NewAgentdClient(agentdSocket)
		poller := stereosd.NewAgentdPoller(client, lifecycle, 100*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go poller.Run(ctx)

		// The poller should update the lifecycle with agent statuses
		Eventually(func() []stereosd.AgentStatusPayload {
			return lifecycle.Health().Agents
		}, 2*time.Second, 50*time.Millisecond).Should(HaveLen(1))

		health := lifecycle.Health()
		Expect(health.Agents[0].Name).To(Equal("opencode"))
		Expect(health.Agents[0].Running).To(BeTrue())

		// Should transition to healthy when agents are running
		Expect(lifecycle.State()).To(Equal(stereosd.StateHealthy))

		cancel()
	})

	It("should handle agentd being unavailable gracefully", func() {
		// No mock server — poller should log and continue
		lifecycle := stereosd.NewLifecycleManager()

		client := stereosd.NewAgentdClient(filepath.Join(tmpDir, "nonexistent.sock"))
		poller := stereosd.NewAgentdPoller(client, lifecycle, 100*time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())

		go poller.Run(ctx)

		// Give the poller time to tick a few times
		time.Sleep(300 * time.Millisecond)

		// Lifecycle should still be booting (no crash)
		Expect(lifecycle.State()).To(Equal(stereosd.StateBooting))

		cancel()
	})
})

// ============================================================================
// LifecycleManager ReplaceAgentStatuses tests
// ============================================================================

var _ = Describe("LifecycleManager ReplaceAgentStatuses", func() {
	It("should replace all agent statuses atomically", func() {
		lm := stereosd.NewLifecycleManager()

		// Add initial agents via UpdateAgentStatus
		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "old-agent",
			Running: true,
		})
		Expect(lm.Health().Agents).To(HaveLen(1))

		// Replace with a new set
		lm.ReplaceAgentStatuses([]stereosd.AgentStatusPayload{
			{Name: "claude-code", Running: true, Session: "claude-main", Restarts: 1},
			{Name: "opencode", Running: false, Error: "stopped"},
		})

		health := lm.Health()
		Expect(health.Agents).To(HaveLen(2))
		Expect(health.Agents[0].Name).To(Equal("claude-code"))
		Expect(health.Agents[0].Restarts).To(Equal(1))
		Expect(health.Agents[1].Name).To(Equal("opencode"))
		Expect(health.Agents[1].Error).To(Equal("stopped"))
	})

	It("should handle empty replacement", func() {
		lm := stereosd.NewLifecycleManager()

		lm.UpdateAgentStatus(stereosd.AgentStatusPayload{
			Name:    "agent",
			Running: true,
		})

		lm.ReplaceAgentStatuses([]stereosd.AgentStatusPayload{})

		Expect(lm.Health().Agents).To(BeEmpty())
	})
})

// ============================================================================
// ShutdownCoordinator tests
// ============================================================================

var _ = Describe("ShutdownCoordinator", func() {
	It("should sync filesystems and call systemctl poweroff", func() {
		cmd := newMockCommander()
		lifecycle := stereosd.NewLifecycleManager()
		mounts := stereosd.NewMountManager(cmd)

		sc := stereosd.NewShutdownCoordinator(mounts, lifecycle, cmd)

		ctx := context.Background()
		err := sc.Execute(ctx, &stereosd.ShutdownPayload{
			Reason: "test",
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(cmd.hasCommand("systemctl")).To(BeTrue())
		Expect(cmd.hasCommand("sync")).To(BeTrue())
		Expect(lifecycle.State()).To(Equal(stereosd.StateShutdown))

		// Verify systemctl poweroff is the only systemctl call —
		// agentd is an independent systemd service and will receive
		// SIGTERM from systemd during poweroff.
		var systemctlCmds [][]string
		for _, c := range cmd.getCommands() {
			if c[0] == "systemctl" {
				systemctlCmds = append(systemctlCmds, c)
			}
		}
		Expect(systemctlCmds).To(HaveLen(1))
		Expect(systemctlCmds[0][1]).To(Equal("poweroff"))
	})
})
