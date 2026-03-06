package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"decentchat/internal/crypto"
)

type Identity struct {
	UserID            string
	KeyPair           *crypto.KeyPair
	TrustedPeers      map[string]string // peer_id -> public_key (trust-on-first-use)
	encryptionKey     [32]byte
}

type identityFile struct {
	X25519Private  []byte            `json:"x25519_private"`
	X25519Public   []byte            `json:"x25519_public"`
	Ed25519Private []byte            `json:"ed25519_private"`
	Ed25519Public  []byte            `json:"ed25519_public"`
	TrustedPeers   map[string]string `json:"trusted_peers"`
}

// LoadOrCreate loads an existing identity or creates a new one
func LoadOrCreate(dataDir string) (*Identity, error) {
	identityPath := filepath.Join(dataDir, "identity.enc")

	// Check if identity file exists
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		return createNewIdentity(dataDir)
	}

	return loadIdentity(dataDir)
}

func createNewIdentity(dataDir string) (*Identity, error) {
	// Generate keypair
	keyPair, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	// Derive user ID
	userID := crypto.DeriveUserID(keyPair.Ed25519Public)

	id := &Identity{
		UserID:       userID,
		KeyPair:      keyPair,
		TrustedPeers: make(map[string]string),
	}

	// Generate encryption key for the identity file
	if _, err := rand.Read(id.encryptionKey[:]); err != nil {
		return nil, err
	}

	// Save the identity
	if err := id.save(dataDir); err != nil {
		return nil, err
	}

	return id, nil
}

func loadIdentity(dataDir string) (*Identity, error) {
	identityPath := filepath.Join(dataDir, "identity.enc")
	keyPath := filepath.Join(dataDir, ".key")

	// Read encryption key
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read encryption key: %w", err)
	}

	var encryptionKey [32]byte
	copy(encryptionKey[:], keyData)

	// Read and decrypt identity file
	encryptedData, err := os.ReadFile(identityPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	decrypted, err := decryptIdentity(encryptedData, encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt identity: %w", err)
	}

	var fileData identityFile
	if err := json.Unmarshal(decrypted, &fileData); err != nil {
		return nil, fmt.Errorf("failed to parse identity: %w", err)
	}

	// Reconstruct keypair
	keyPair := &crypto.KeyPair{}
	copy(keyPair.X25519Private[:], fileData.X25519Private)
	copy(keyPair.X25519Public[:], fileData.X25519Public)
	keyPair.Ed25519Public = fileData.Ed25519Public
	keyPair.Ed25519Private = fileData.Ed25519Private

	userID := crypto.DeriveUserID(keyPair.Ed25519Public)

	return &Identity{
		UserID:        userID,
		KeyPair:       keyPair,
		TrustedPeers:  fileData.TrustedPeers,
		encryptionKey: encryptionKey,
	}, nil
}

func (id *Identity) save(dataDir string) error {
	identityPath := filepath.Join(dataDir, "identity.enc")
	keyPath := filepath.Join(dataDir, ".key")

	// Prepare file data
	fileData := identityFile{
		X25519Private:  id.KeyPair.X25519Private[:],
		X25519Public:   id.KeyPair.X25519Public[:],
		Ed25519Private: id.KeyPair.Ed25519Private,
		Ed25519Public:  id.KeyPair.Ed25519Public,
		TrustedPeers:   id.TrustedPeers,
	}

	jsonData, err := json.Marshal(fileData)
	if err != nil {
		return err
	}

	// Encrypt the identity
	encrypted, err := encryptIdentity(jsonData, id.encryptionKey)
	if err != nil {
		return err
	}

	// Save encryption key (with restricted permissions)
	if err := os.WriteFile(keyPath, id.encryptionKey[:], 0600); err != nil {
		return err
	}

	// Save encrypted identity
	return os.WriteFile(identityPath, encrypted, 0600)
}

// ShortID returns the first 8 characters of the user ID
func (id *Identity) ShortID() string {
	if len(id.UserID) >= 8 {
		return id.UserID[:8]
	}
	return id.UserID
}

// TrustPeer adds a peer to the trusted list (trust-on-first-use)
func (id *Identity) TrustPeer(peerID, publicKey string) {
	id.TrustedPeers[peerID] = publicKey
}

// IsPeerTrusted checks if a peer is trusted
func (id *Identity) IsPeerTrusted(peerID, publicKey string) bool {
	trustedKey, exists := id.TrustedPeers[peerID]
	if !exists {
		return false
	}
	return trustedKey == publicKey
}

// EncryptIdentity encrypts data for identity storage
func encryptIdentity(plaintext []byte, key [32]byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptIdentity decrypts data from identity storage
func decryptIdentity(ciphertext []byte, key [32]byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// DeriveKeyFromPassword derives a key from a password using SHA256
func DeriveKeyFromPassword(password string) [32]byte {
	hash := sha256.Sum256([]byte(password))
	return hash
}
