package network

import (
	"encoding/json"
	"testing"

	"decentchat/internal/crypto"
	"decentchat/internal/identity"
)

// Ensures the Message envelope (including the new Type field) survives
// JSON marshal/unmarshal — the shape used by encrypt/sign on the wire.
func TestMessageEncodingRoundTrip(t *testing.T) {
	msg := Message{
		Type:      "file_chunk",
		Content:   []byte("0123456789abcdefpayload"),
		Signature: []byte("signature"),
		Timestamp: 1700000000,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Message
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Type != "file_chunk" {
		t.Errorf("Type = %q, want %q", out.Type, "file_chunk")
	}
	if string(out.Content) != string(msg.Content) {
		t.Errorf("Content mismatch: %q", string(out.Content))
	}
	if out.Timestamp != msg.Timestamp {
		t.Errorf("Timestamp = %d, want %d", out.Timestamp, msg.Timestamp)
	}

	// Legacy chat messages (no Type) must decode as chat
	legacy, _ := json.Marshal(Message{Content: []byte("hi")})
	var legacyOut Message
	_ = json.Unmarshal(legacy, &legacyOut)
	if legacyOut.Type != "" {
		t.Errorf("legacy Type = %q, want empty", legacyOut.Type)
	}
}

// SendFile must refuse to run when there is no active peer connection.
func TestSendFileNotConnected(t *testing.T) {
	id := &identity.Identity{
		UserID:  "testuser",
		KeyPair: &crypto.KeyPair{},
	}
	mgr := NewManager(id, t.TempDir())

	if _, err := mgr.SendFile("does-not-exist.txt"); err == nil {
		t.Fatal("SendFile with no connection should fail")
	}
}

// Chunk paragraph sizing keeps each chunk well under typical websocket
// frame limits after JSON encoding of the file ID prefix.
func TestChunkConstants(t *testing.T) {
	if fileChunkSize <= 0 || fileChunkSize > 128*1024 {
		t.Errorf("unexpected chunk size: %d", fileChunkSize)
	}
	if fileIDBytes != 16 {
		t.Errorf("file id must be 16 bytes, got %d", fileIDBytes)
	}
}
