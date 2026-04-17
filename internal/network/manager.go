package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"decentchat/internal/crypto"
	"decentchat/internal/identity"

	"github.com/gorilla/websocket"
)

// Message represents an encrypted message
type Message struct {
	Content   []byte `json:"content"`
	Signature []byte `json:"signature"`
	Timestamp int64  `json:"timestamp"`
}

// Manager handles WebSocket connections and Cloudflare Tunnels
type Manager struct {
	identity *identity.Identity
	dataDir  string

	// Callbacks
	onMessage            func(string)
	onConnected          func()
	onDisconnected       func()
	onIncomingConnection func(peerID string)

	// State
	mu            sync.Mutex
	writeMu       sync.Mutex
	connected     bool
	tunnelURL     string
	peerPublicKey [32]byte
	sharedSecret  [32]byte
	peerIDKey     []byte // Ed25519 public key for verification
	keepAliveStop chan struct{}

	// Active connection
	conn *websocket.Conn

	// Server Components
	server   *http.Server
	listener net.Listener
	tunnel   *exec.Cmd

	// Dialing context
	ctx       context.Context
	ctxCancel context.CancelFunc
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for the tunnel
	},
}

const keepAliveInterval = 45 * time.Second

// NewManager creates a new network manager
func NewManager(id *identity.Identity, dataDir string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		identity:  id,
		dataDir:   dataDir,
		ctx:       ctx,
		ctxCancel: cancel,
	}
}

// SetCallbacks sets the callback functions
func (m *Manager) SetCallbacks(onMsg func(string), onConn func(), onDisconn func(), onIncoming func(string)) {
	m.onMessage = onMsg
	m.onConnected = onConn
	m.onDisconnected = onDisconn
	m.onIncomingConnection = onIncoming
}

// StartServer starts the local HTTP server and the Cloudflare Tunnel
func (m *Manager) StartServer() (string, error) {
	// 1. Start local listener on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("failed to start listener: %w", err)
	}
	m.listener = listener

	port := listener.Addr().(*net.TCPAddr).Port

	// 2. Start HTTP Server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", m.handleWebSocket)

	m.server = &http.Server{Handler: mux}
	go func() {
		_ = m.server.Serve(m.listener)
	}()

	// 3. Start Cloudflare Tunnel
	tunnelURL, err := m.startCloudflareTunnel(port)
	if err != nil {
		return "", err
	}

	m.tunnelURL = tunnelURL
	return tunnelURL, nil
}

// startCloudflareTunnel spawns the cloudflared process and extracts the TryCloudflare URL
func (m *Manager) startCloudflareTunnel(port int) (string, error) {
	binaryPath, err := m.ensureCloudflared()
	if err != nil {
		return "", fmt.Errorf("failed to prepare cloudflared: %w", err)
	}

	cmd := exec.CommandContext(m.ctx, binaryPath, "tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", port))

	// cloudflared writes logs to stderr
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start cloudflared. Is it installed? %w", err)
	}
	m.tunnel = cmd

	// Read stderr to find the URL
	urlChan := make(chan string)
	errChan := make(chan error)

	go func() {
		scanner := bufio.NewScanner(stderr)
		urlRegex := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
		for scanner.Scan() {
			line := scanner.Text()
			if match := urlRegex.FindString(line); match != "" {
				// Convert to wss
				wsURL := strings.Replace(match, "https://", "wss://", 1) + "/ws"
				urlChan <- wsURL
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errChan <- err
		} else {
			errChan <- fmt.Errorf("cloudflared exited without providing a URL")
		}
	}()

	select {
	case url := <-urlChan:
		return url, nil
	case err := <-errChan:
		return "", err
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("timeout waiting for cloudflare tunnel url")
	}
}

// ensureCloudflared checks if the binary exists, downloads if missing, and returns path
func (m *Manager) ensureCloudflared() (string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	binName := "cloudflared"
	if osName == "windows" {
		binName += ".exe"
	}

	binPath := filepath.Join(m.dataDir, binName)

	// Check if already exists
	if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
		return binPath, nil
	}

	// Try checking if it's already in the system PATH
	if sysPath, err := exec.LookPath("cloudflared"); err == nil {
		return sysPath, nil
	}

	// Not found, downloading matching binary
	fmt.Printf("\nCloudflared binary not found locally or in PATH.\nDownloading for %s-%s... Please wait.\n", osName, arch)

	// Determine download URL
	var downloadURL string
	if osName == "windows" && arch == "amd64" {
		downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"
	} else if osName == "linux" && arch == "amd64" {
		downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"
	} else if osName == "linux" && arch == "arm64" {
		downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64"
	} else if osName == "darwin" && arch == "amd64" {
		downloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-amd64.tgz" // Darwin needs extraction realistically, but this serves as placeholder
	} else {
		return "", fmt.Errorf("auto-download not supported for %s-%s; please install cloudflared manually", osName, arch)
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to download cloudflared: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: HTTP %d", resp.StatusCode)
	}

	// Make sure datadir exists
	if err := os.MkdirAll(m.dataDir, 0755); err != nil {
		return "", err
	}

	outFile, err := os.OpenFile(binPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return "", err
	}

	fmt.Println("Download complete!")

	return binPath, nil
}

// ConnectToPeer dials a peer's tunnel URL
func (m *Manager) ConnectToPeer(peerID string, peerTunnelURL string, peerEncKeyBase64 string, peerIdKeyBase64 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected {
		return fmt.Errorf("already connected")
	}

	// Wait, we need the peer's keys to establish trust/shared secret
	peerEncKey, err := crypto.DecodePublicKey(peerEncKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode peer enc key: %w", err)
	}

	peerIDKey, err := crypto.DecodeEd25519PublicKey(peerIdKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode peer id key: %w", err)
	}

	m.peerPublicKey = peerEncKey
	m.peerIDKey = peerIDKey
	m.sharedSecret = crypto.ComputeSharedSecret(m.identity.KeyPair.X25519Private, peerEncKey)

	// Dial the WebSocket
	// Add our identity header so the receiver knows who is calling
	header := http.Header{}
	header.Add("X-Peer-ID", m.identity.UserID)

	conn, _, err := websocket.DefaultDialer.Dial(peerTunnelURL, header)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	m.conn = conn
	m.connected = true
	m.startKeepAliveLocked()

	// Now we are connected, start reading loop
	go m.readLoop(conn)

	if m.onConnected != nil {
		go m.onConnected()
	}

	return nil
}

// handleWebSocket is the HTTP handler for incoming connections
func (m *Manager) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	peerID := r.Header.Get("X-Peer-ID")
	if peerID == "" {
		http.Error(w, "missing X-Peer-ID", http.StatusBadRequest)
		return
	}

	// For simplicity in this architecture, we upgrade the connection immediately,
	// but we don't consider it "fully connected" (i.e. we don't trigger onConnected)
	// until the user physically accepts.
	// The plan: UPGRADE IT. Wait for /accept.
	// Actually, wait. The requirement says User B receives prompt and types /accept.
	// We can upgrade, but keep it in a "pending" queue, and trigger onIncomingConnection.

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	m.mu.Lock()
	if m.connected {
		m.mu.Unlock()
		conn.Close()
		return
	}
	m.conn = conn // Temporarily hold it here, but not fully "connected"
	m.mu.Unlock()

	// Notify UI of incoming connection
	if m.onIncomingConnection != nil {
		m.onIncomingConnection(peerID)
	}

	// We do NOT start the read loop until accepted.
}

// AcceptConnection completes the connection from the receiver's side
func (m *Manager) AcceptConnection(peerEncKeyBase64 string, peerIdKeyBase64 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conn == nil {
		return fmt.Errorf("no pending connection")
	}

	peerEncKey, err := crypto.DecodePublicKey(peerEncKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode peer enc key: %w", err)
	}

	peerIDKey, err := crypto.DecodeEd25519PublicKey(peerIdKeyBase64)
	if err != nil {
		return fmt.Errorf("failed to decode peer id key: %w", err)
	}

	m.peerPublicKey = peerEncKey
	m.peerIDKey = peerIDKey
	m.sharedSecret = crypto.ComputeSharedSecret(m.identity.KeyPair.X25519Private, peerEncKey)
	m.connected = true
	m.startKeepAliveLocked()

	go m.readLoop(m.conn)

	if m.onConnected != nil {
		go m.onConnected()
	}

	return nil
}

// DeclineConnection closes the pending connection
func (m *Manager) DeclineConnection() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conn != nil && !m.connected {
		m.conn.Close()
		m.conn = nil
	}
}

// readLoop reads messages from the websocket
func (m *Manager) readLoop(conn *websocket.Conn) {
	defer func() {
		m.CloseConnection()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		m.mu.Lock()
		secret := m.sharedSecret
		peerIDKey := m.peerIDKey
		m.mu.Unlock()

		decrypted, err := crypto.Decrypt(data, secret)
		if err != nil {
			if m.onMessage != nil {
				m.onMessage(fmt.Sprintf("[Error: Failed to decrypt message: %v]", err))
			}
			continue
		}

		var message Message
		if err := json.Unmarshal(decrypted, &message); err != nil {
			if m.onMessage != nil {
				m.onMessage(fmt.Sprintf("[Error: Failed to parse message: %v]", err))
			}
			continue
		}

		// Verify signature
		if !crypto.Verify(message.Content, message.Signature, peerIDKey) {
			if m.onMessage != nil {
				m.onMessage("[Warning: Message signature verification failed]")
			}
			continue
		}

		if m.onMessage != nil {
			m.onMessage(string(message.Content))
		}
	}
}

// SendMessage sends an encrypted message
func (m *Manager) SendMessage(text string) error {
	m.mu.Lock()
	if !m.connected || m.conn == nil {
		m.mu.Unlock()
		return fmt.Errorf("not connected to peer")
	}
	conn := m.conn
	secret := m.sharedSecret
	privKey := m.identity.KeyPair.Ed25519Private
	m.mu.Unlock()

	msg := Message{
		Content:   []byte(text),
		Timestamp: time.Now().Unix(),
	}

	msg.Signature = crypto.Sign(msg.Content, privKey)

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	encrypted, err := crypto.Encrypt(msgBytes, secret)
	if err != nil {
		return fmt.Errorf("failed to encrypt message: %w", err)
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, encrypted)
}

// CloseConnection closes only the active websocket connection (not the server)
func (m *Manager) CloseConnection() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}

	wasConnected := m.connected
	m.connected = false
	m.stopKeepAliveLocked()
	m.sharedSecret = [32]byte{}
	m.peerPublicKey = [32]byte{}

	if wasConnected && m.onDisconnected != nil {
		go m.onDisconnected()
	}
}

func (m *Manager) startKeepAliveLocked() {
	m.stopKeepAliveLocked()
	stop := make(chan struct{})
	m.keepAliveStop = stop
	go m.keepAliveLoop(stop)
}

func (m *Manager) stopKeepAliveLocked() {
	if m.keepAliveStop != nil {
		close(m.keepAliveStop)
		m.keepAliveStop = nil
	}
}

func (m *Manager) keepAliveLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			if m.keepAliveStop != stop || !m.connected || m.conn == nil {
				m.mu.Unlock()
				return
			}
			conn := m.conn
			m.mu.Unlock()

			m.writeMu.Lock()
			err := conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(5*time.Second))
			m.writeMu.Unlock()
			if err != nil {
				m.CloseConnection()
				return
			}
		case <-stop:
			return
		case <-m.ctx.Done():
			return
		}
	}
}

// Shutdown shuts down the entire manager (server, tunnel, connections)
func (m *Manager) Shutdown() {
	m.CloseConnection()

	m.ctxCancel()

	if m.server != nil {
		m.server.Close()
	}

	if m.tunnel != nil && m.tunnel.Process != nil {
		m.tunnel.Process.Kill()
	}
}

// IsConnected returns whether there is an active connection
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}
