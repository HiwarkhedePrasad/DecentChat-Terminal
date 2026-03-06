package webrtc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"decentchat/internal/crypto"
	"decentchat/internal/identity"
	"decentchat/internal/signaling"

	"github.com/pion/webrtc/v3"
)

// Message represents an encrypted message
type Message struct {
	Content   []byte `json:"content"`
	Signature []byte `json:"signature"`
	Timestamp int64  `json:"timestamp"`
}

// Manager handles WebRTC connections
type Manager struct {
	identity        *identity.Identity
	api             *webrtc.API
	peerConnection  *webrtc.PeerConnection
	dataChannel     *webrtc.DataChannel
	signalingClient *signaling.Client

	// Callbacks
	onMessage      func(string)
	onConnected    func()
	onDisconnected func()

	// State
	mu             sync.Mutex
	connected      bool
	peerPublicKey  [32]byte
	sharedSecret   [32]byte
	peerIDKey      []byte // Ed25519 public key for verification

	// ICE servers
	iceServers []webrtc.ICEServer
}

// NewManager creates a new WebRTC manager
func NewManager(id *identity.Identity) *Manager {
	// Create API with default settings
	api := webrtc.NewAPI()

	return &Manager{
		identity: id,
		api:      api,
		iceServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
				},
			},
		},
	}
}

// SetSignalingClient sets the signaling client
func (m *Manager) SetSignalingClient(client *signaling.Client) {
	m.signalingClient = client
}

// SetCallbacks sets the callback functions
func (m *Manager) SetCallbacks(onMessage func(string), onConnected func(), onDisconnected func()) {
	m.onMessage = onMessage
	m.onConnected = onConnected
	m.onDisconnected = onDisconnected
}

// CreateOffer creates a WebRTC offer for connecting to a peer
func (m *Manager) CreateOffer(peer *signaling.User) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Decode peer's public keys
	peerEncKey, err := crypto.DecodePublicKey(peer.PublicEncKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode peer encryption key: %w", err)
	}

	m.peerPublicKey = peerEncKey
	m.peerIDKey, err = crypto.DecodeEd25519PublicKey(peer.PublicIdentityKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode peer identity key: %w", err)
	}

	// Compute shared secret
	m.sharedSecret = crypto.ComputeSharedSecret(m.identity.KeyPair.X25519Private, peerEncKey)

	// Create peer connection
	config := webrtc.Configuration{
		ICEServers: m.iceServers,
	}

	pc, err := m.api.NewPeerConnection(config)
	if err != nil {
		return "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	m.peerConnection = pc

	// Create data channel
	dc, err := pc.CreateDataChannel("chat", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create data channel: %w", err)
	}

	m.dataChannel = dc
	m.setupDataChannelHandlers(dc)

	// Set up connection state handlers
	m.setupConnectionHandlers(pc)

	// Create offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	<-webrtc.GatheringCompletePromise(pc)

	// Encode offer
	encoded, err := encodeSDP(*pc.LocalDescription())
	if err != nil {
		return "", err
	}

	return encoded, nil
}

// HandleOffer handles an incoming offer and creates an answer
func (m *Manager) HandleOffer(offer string, peer *signaling.User) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Decode peer's public keys
	peerEncKey, err := crypto.DecodePublicKey(peer.PublicEncKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode peer encryption key: %w", err)
	}

	m.peerPublicKey = peerEncKey
	m.peerIDKey, err = crypto.DecodeEd25519PublicKey(peer.PublicIdentityKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode peer identity key: %w", err)
	}

	// Compute shared secret
	m.sharedSecret = crypto.ComputeSharedSecret(m.identity.KeyPair.X25519Private, peerEncKey)

	// Decode offer
	sdp, err := decodeSDP(offer)
	if err != nil {
		return "", fmt.Errorf("failed to decode offer: %w", err)
	}

	// Create peer connection
	config := webrtc.Configuration{
		ICEServers: m.iceServers,
	}

	pc, err := m.api.NewPeerConnection(config)
	if err != nil {
		return "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	m.peerConnection = pc
	m.setupConnectionHandlers(pc)

	// Handle incoming data channel
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		m.dataChannel = dc
		m.setupDataChannelHandlers(dc)
	})

	// Set remote description
	if err := pc.SetRemoteDescription(sdp); err != nil {
		return "", fmt.Errorf("failed to set remote description: %w", err)
	}

	// Create answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	<-webrtc.GatheringCompletePromise(pc)

	// Encode answer
	encoded, err := encodeSDP(*pc.LocalDescription())
	if err != nil {
		return "", err
	}

	return encoded, nil
}

// HandleAnswer handles an incoming answer
func (m *Manager) HandleAnswer(answer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.peerConnection == nil {
		return fmt.Errorf("no active peer connection")
	}

	sdp, err := decodeSDP(answer)
	if err != nil {
		return fmt.Errorf("failed to decode answer: %w", err)
	}

	return m.peerConnection.SetRemoteDescription(sdp)
}

// SendMessage sends an encrypted message
func (m *Manager) SendMessage(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dataChannel == nil || !m.connected {
		return fmt.Errorf("not connected to peer")
	}

	// Create message with timestamp
	msg := Message{
		Content:   []byte(text),
		Timestamp: time.Now().Unix(),
	}

	// Sign the message
	msg.Signature = crypto.Sign(msg.Content, m.identity.KeyPair.Ed25519Private)

	// Encrypt the message
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	encrypted, err := crypto.Encrypt(msgBytes, m.sharedSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt message: %w", err)
	}

	// Send the encrypted message
	return m.dataChannel.Send(encrypted)
}

// Close closes the current connection
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connected = false

	if m.peerConnection != nil {
		if err := m.peerConnection.Close(); err != nil {
			return err
		}
		m.peerConnection = nil
	}

	m.dataChannel = nil
	m.sharedSecret = [32]byte{}
	m.peerPublicKey = [32]byte{}

	return nil
}

// IsConnected returns whether there is an active connection
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// GetPeerPublicKey returns the peer's public key for trust verification
func (m *Manager) GetPeerPublicKey() string {
	if m.peerIDKey != nil {
		return base64.StdEncoding.EncodeToString(m.peerIDKey)
	}
	return ""
}

func (m *Manager) setupDataChannelHandlers(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		m.mu.Lock()
		m.connected = true
		m.mu.Unlock()

		if m.onConnected != nil {
			m.onConnected()
		}
	})

	dc.OnClose(func() {
		m.mu.Lock()
		m.connected = false
		m.mu.Unlock()

		if m.onDisconnected != nil {
			m.onDisconnected()
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Decrypt message
		decrypted, err := crypto.Decrypt(msg.Data, m.sharedSecret)
		if err != nil {
			if m.onMessage != nil {
				m.onMessage(fmt.Sprintf("[Error: Failed to decrypt message: %v]", err))
			}
			return
		}

		var message Message
		if err := json.Unmarshal(decrypted, &message); err != nil {
			if m.onMessage != nil {
				m.onMessage(fmt.Sprintf("[Error: Failed to parse message: %v]", err))
			}
			return
		}

		// Verify signature
		if !crypto.Verify(message.Content, message.Signature, m.peerIDKey) {
			if m.onMessage != nil {
				m.onMessage("[Warning: Message signature verification failed]")
			}
			return
		}

		// Call the message callback
		if m.onMessage != nil {
			m.onMessage(string(message.Content))
		}
	})
}

func (m *Manager) setupConnectionHandlers(pc *webrtc.PeerConnection) {
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			// Connection established
		case webrtc.PeerConnectionStateDisconnected:
			m.mu.Lock()
			m.connected = false
			m.mu.Unlock()

			if m.onDisconnected != nil {
				m.onDisconnected()
			}
		case webrtc.PeerConnectionStateFailed:
			m.mu.Lock()
			m.connected = false
			m.mu.Unlock()

			if m.onDisconnected != nil {
				m.onDisconnected()
			}
		}
	})
}

// encodeSDP encodes an SDP to base64
func encodeSDP(sdp webrtc.SessionDescription) (string, error) {
	bytes, err := json.Marshal(sdp)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

// decodeSDP decodes a base64 SDP
func decodeSDP(encoded string) (webrtc.SessionDescription, error) {
	var sdp webrtc.SessionDescription

	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return sdp, err
	}

	err = json.Unmarshal(bytes, &sdp)
	return sdp, err
}
