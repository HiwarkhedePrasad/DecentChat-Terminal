package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"

	"golang.org/x/crypto/curve25519"
)

// KeyPair holds both X25519 and Ed25519 keypairs
type KeyPair struct {
	// X25519 keys for key exchange
	X25519Public  [32]byte
	X25519Private [32]byte

	// Ed25519 keys for signing
	Ed25519Public  ed25519.PublicKey
	Ed25519Private ed25519.PrivateKey
}

// GenerateKeyPair creates new X25519 and Ed25519 keypairs
func GenerateKeyPair() (*KeyPair, error) {
	kp := &KeyPair{}

	// Generate X25519 keypair for key exchange
	if _, err := rand.Read(kp.X25519Private[:]); err != nil {
		return nil, err
	}
	curve25519.ScalarBaseMult(&kp.X25519Public, &kp.X25519Private)

	// Generate Ed25519 keypair for signing
	var err error
	kp.Ed25519Public, kp.Ed25519Private, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	return kp, nil
}

// DeriveUserID creates a user ID from the public identity key
func DeriveUserID(publicKey ed25519.PublicKey) string {
	hash := sha256.Sum256(publicKey)
	return hex.EncodeToString(hash[:8])
}

// ComputeSharedSecret performs X25519 key exchange
func ComputeSharedSecret(privateKey [32]byte, publicKey [32]byte) [32]byte {
	var shared [32]byte
	curve25519.ScalarMult(&shared, &privateKey, &publicKey)
	return shared
}

// Encrypt encrypts data using AES-256-GCM
func Encrypt(plaintext []byte, key [32]byte) ([]byte, error) {
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

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts data using AES-256-GCM
func Decrypt(ciphertext []byte, key [32]byte) ([]byte, error) {
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
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// Sign signs a message with Ed25519 private key
func Sign(message []byte, privateKey ed25519.PrivateKey) []byte {
	return ed25519.Sign(privateKey, message)
}

// Verify verifies an Ed25519 signature
func Verify(message, signature []byte, publicKey ed25519.PublicKey) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// EncodePublicKey encodes a public key to base64 string
func EncodePublicKey(key [32]byte) string {
	return base64.StdEncoding.EncodeToString(key[:])
}

// DecodePublicKey decodes a base64 string to public key
func DecodePublicKey(encoded string) ([32]byte, error) {
	var key [32]byte
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return key, err
	}
	copy(key[:], decoded)
	return key, nil
}

// EncodeEd25519PublicKey encodes an Ed25519 public key to base64 string
func EncodeEd25519PublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

// DecodeEd25519PublicKey decodes a base64 string to Ed25519 public key
func DecodeEd25519PublicKey(encoded string) (ed25519.PublicKey, error) {
	return base64.StdEncoding.DecodeString(encoded)
}
