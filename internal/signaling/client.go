package signaling

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"decentchat/internal/crypto"
	"decentchat/internal/identity"
)

// User represents a user in the signaling server
type User struct {
	UserID            string    `json:"user_id"`
	PublicIdentityKey string    `json:"public_identity_key"`
	PublicEncKey      string    `json:"public_enc_key"`
	WebRTCOffer       string    `json:"webrtc_offer"`
	WebRTCAnswer      string    `json:"webrtc_answer"`
	OnlineStatus      bool      `json:"online_status"`
	LastSeen          time.Time `json:"last_seen"`
}

// Client handles communication with the Supabase signaling server
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	identity   *identity.Identity
}

// NewClient creates a new signaling client
func NewClient(baseURL, apiKey string, id *identity.Identity) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		identity:   id,
	}
}

// Register registers or updates the user in the signaling server
func (c *Client) Register() error {
	user := map[string]interface{}{
		"user_id":             c.identity.UserID,
		"public_identity_key": crypto.EncodeEd25519PublicKey(c.identity.KeyPair.Ed25519Public),
		"public_enc_key":      crypto.EncodePublicKey(c.identity.KeyPair.X25519Public),
		"online_status":       true,
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// SetOffline marks the user as offline
func (c *Client) SetOffline(userID string) error {
	user := map[string]interface{}{
		"user_id":             userID,
		"public_identity_key": crypto.EncodeEd25519PublicKey(c.identity.KeyPair.Ed25519Public),
		"public_enc_key":      crypto.EncodePublicKey(c.identity.KeyPair.X25519Public),
		"online_status":       false,
		"webrtc_offer":        "",
		"webrtc_answer":       "",
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// UpdateOffer updates the WebRTC offer for the user
func (c *Client) UpdateOffer(offer string) error {
	user := map[string]interface{}{
		"user_id":             c.identity.UserID,
		"public_identity_key": crypto.EncodeEd25519PublicKey(c.identity.KeyPair.Ed25519Public),
		"public_enc_key":      crypto.EncodePublicKey(c.identity.KeyPair.X25519Public),
		"webrtc_offer":        offer,
		"webrtc_answer":       "",
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// UpdateAnswer updates the WebRTC answer for the user
func (c *Client) UpdateAnswer(answer string) error {
	user := map[string]interface{}{
		"user_id":             c.identity.UserID,
		"public_identity_key": crypto.EncodeEd25519PublicKey(c.identity.KeyPair.Ed25519Public),
		"public_enc_key":      crypto.EncodePublicKey(c.identity.KeyPair.X25519Public),
		"webrtc_offer":        "",
		"webrtc_answer":       answer,
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// ClearOffer clears the WebRTC offer and answer
func (c *Client) ClearOffer() error {
	user := map[string]interface{}{
		"user_id":             c.identity.UserID,
		"public_identity_key": crypto.EncodeEd25519PublicKey(c.identity.KeyPair.Ed25519Public),
		"public_enc_key":      crypto.EncodePublicKey(c.identity.KeyPair.X25519Public),
		"webrtc_offer":        "",
		"webrtc_answer":       "",
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// GetOnlineUsers retrieves all online users
func (c *Client) GetOnlineUsers() ([]User, error) {
	url := fmt.Sprintf("%s/rest/v1/users?online_status=eq.true&select=*", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get users: %s", string(body))
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, err
	}

	return users, nil
}

// GetUser retrieves a specific user by ID
func (c *Client) GetUser(userID string) (*User, error) {
	url := fmt.Sprintf("%s/rest/v1/users?user_id=eq.%s&select=*", c.baseURL, userID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get user: %s", string(body))
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, err
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("user not found")
	}

	return &users[0], nil
}

// SetOfferForPeer stores an offer for a specific peer (caller stores offer in callee's record)
func (c *Client) SetOfferForPeer(peerID, offer string) error {
	// First, fetch the peer's current record to preserve their keys
	peer, err := c.GetUser(peerID)
	if err != nil {
		return fmt.Errorf("could not fetch peer record: %v", err)
	}

	user := map[string]interface{}{
		"user_id":             peerID,
		"public_identity_key": peer.PublicIdentityKey,
		"public_enc_key":      peer.PublicEncKey,
		"webrtc_offer":        offer,
		"webrtc_answer":       "",
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

// SetAnswerForPeer stores an answer for a specific peer
func (c *Client) SetAnswerForPeer(peerID, answer string) error {
	// First, fetch the peer's current record to preserve their keys
	peer, err := c.GetUser(peerID)
	if err != nil {
		return fmt.Errorf("could not fetch peer record: %v", err)
	}

	user := map[string]interface{}{
		"user_id":             peerID,
		"public_identity_key": peer.PublicIdentityKey,
		"public_enc_key":      peer.PublicEncKey,
		"webrtc_offer":        "",
		"webrtc_answer":       answer,
		"last_seen":           time.Now().UTC().Format(time.RFC3339),
	}

	return c.upsertUser(user)
}

func (c *Client) upsertUser(user map[string]interface{}) error {
	url := fmt.Sprintf("%s/rest/v1/users?on_conflict=user_id", c.baseURL)

	body, err := json.Marshal(user)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	c.setHeaders(req)
	req.Header.Set("Prefer", "resolution=merge-duplicates")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upsert user: %s", string(respBody))
	}

	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("apikey", c.apiKey)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
}
