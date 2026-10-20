package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"decentchat/internal/crypto"
	"decentchat/internal/identity"
)

// newTestIdentity creates a real keypair-backed identity for testing.
func newTestIdentity(t *testing.T, userID string) *identity.Identity {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &identity.Identity{UserID: userID, KeyPair: kp}
}

// TestChatAndFileTransferRoundTrip wires two Managers together over a
// plain local websocket (no cloudflared) and exercises: chat, a full
// file transfer with hash verification, and chat staying responsive
// while a large file is being transferred.
func TestChatAndFileTransferRoundTrip(t *testing.T) {
	userA := newTestIdentity(t, "aaaaaaaaaaaaaaaa")
	userB := newTestIdentity(t, "bbbbbbbbbbbbbbbb")

	mgrB := NewManager(userB, t.TempDir())
	defer mgrB.Shutdown()

	// Start the receiver's websocket server directly (bypasses tunnel).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: nil}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", mgrB.handleWebSocket)
	srv.Handler = mux
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	url := "ws://" + ln.Addr().String() + "/ws"

	// Receiver side: auto-accept anything.
	mgrB.SetCallbacks(
		func(msg string) {},
		func() {},
		func() {},
		func(peerID string) {
			go func() {
				time.Sleep(20 * time.Millisecond)
				_ = mgrB.AcceptConnection(
					crypto.EncodePublicKey(userA.KeyPair.X25519Public),
					crypto.EncodeEd25519PublicKey(userA.KeyPair.Ed25519Public),
				)
			}()
		},
	)

	type fileDone struct {
		result FileResult
	}
	fileDoneB := make(chan FileResult, 8)
	var chatMu sync.Mutex
	var chatsB []string

	mgrB.SetFileCallbacks(
		func(offer FileOfferInfo) {},
		func(p FileProgress) {},
		func(r FileResult) { fileDoneB <- r },
	)
	mgrB.onMessage = func(msg string) {
		chatMu.Lock()
		chatsB = append(chatsB, msg)
		chatMu.Unlock()
	}

	// Caller side.
	mgrA := NewManager(userA, t.TempDir())
	defer mgrA.Shutdown()

	if err := mgrA.ConnectToPeer(
		userB.UserID, url,
		crypto.EncodePublicKey(userB.KeyPair.X25519Public),
		crypto.EncodeEd25519PublicKey(userB.KeyPair.Ed25519Public),
	); err != nil {
		t.Fatalf("ConnectToPeer: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !mgrA.IsConnected() {
		time.Sleep(50 * time.Millisecond)
	}
	if !mgrA.IsConnected() {
		t.Fatal("failed to connect manager A to manager B")
	}

	// 1. Plain chat.
	if err := mgrA.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// 2. A ~800KB file; mid-transfer chat must still flow.
	payload := make([]byte, 800*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	srcPath := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(srcPath, payload, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	offer, err := mgrA.SendFile(srcPath)
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if offer.SHA256 != hexOfBytes(payload) {
		t.Fatal("offer hash mismatch")
	}

	// Chat in the middle of the transfer.
	if err := mgrA.SendMessage("during-transfer"); err != nil {
		t.Fatalf("mid-transfer SendMessage: %v", err)
	}

	// 3. Receiver completes the transfer.
	deadline = time.Now().Add(30 * time.Second)
	var done FileResult
	for {
		var r FileResult
		select {
		case r = <-fileDoneB:
		case <-time.After(time.Until(deadline)):
			t.Fatal("timed out waiting for file transfer to finish")
		}
		if r.OK && !r.Outgoing {
			done = r
			break
		}
		if !r.OK && !r.Outgoing {
			t.Fatalf("receiver transfer failed: %s", r.Message)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for receiver completion")
		}
	}

	received, err := os.ReadFile(done.SavePath)
	if err != nil {
		t.Fatalf("read spooled file: %v", err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("received %d bytes, want %d", len(received), len(payload))
	}
	sum := sha256.Sum256(received)
	if hex.EncodeToString(sum[:]) != offer.SHA256 {
		t.Fatal("received file hash mismatch")
	}

	// 4. Both chat messages (before + mid-transfer) arrived at B.
	time.Sleep(300 * time.Millisecond)
	chatMu.Lock()
	defer chatMu.Unlock()
	if len(chatsB) < 2 {
		t.Fatalf("expected 2 chat messages at receiver, got %d: %v", len(chatsB), chatsB)
	}
	if !contains(chatsB, "hello") || !contains(chatsB, "during-transfer") {
		t.Fatalf("chat messages lost: %v", chatsB)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// hexOfBytes is a small test helper mirroring the sha256 formatting used
// on the wire.
func hexOfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
