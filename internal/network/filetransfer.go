package network

import (
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// FileOfferInfo describes a file that is about to be transferred.
type FileOfferInfo struct {
	FileID      string `json:"file_id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	TotalChunks int    `json:"total_chunks"`
	SHA256      string `json:"sha256"`
}

// FileProgress is a progress update for an in-flight transfer.
type FileProgress struct {
	FileID   string
	Name     string
	Sent     int64
	Total    int64
	Outgoing bool
}

// FileResult signals the final state of a transfer.
type FileResult struct {
	FileID   string
	Name     string
	SavePath string // receiver only: path holding the fully reassembled file
	Outgoing bool
	OK       bool
	Message  string // error description when OK == false
}

const (
	fileChunkSize = 64 * 1024 // bytes per chunk
	// Small pacing delay between chunks so chat messages keep flowing
	// while a large file transfer is running (non-blocking chat).
	fileChunkPause = 20 * time.Millisecond
	// Fixed-length binary file ID prefix on every chunk.
	fileIDBytes = 16
	// Notify the UI roughly every 1 MB instead of every chunk.
	fileReportStep = int64(1 << 20)
)

type fileSendState struct {
	info         *FileOfferInfo
	file         *os.File
	path         string
	sent         int64
	lastNotified int64
}

type fileRecvState struct {
	info         *FileOfferInfo
	out          *os.File
	spoolPath    string
	received     int64
	lastNotified int64
}

var unsafeFilename = regexp.MustCompile(`[^a-zA-Z0-9._+\-()\[\] ]`)

// SetFileCallbacks wires up file-transfer notifications for the UI:
// an offer was received, transfer progress, and transfer completion.
func (m *Manager) SetFileCallbacks(onOffer func(FileOfferInfo), onProgress func(FileProgress), onDone func(FileResult)) {
	m.fileMu.Lock()
	m.onFileOffer = onOffer
	m.onFileProgress = onProgress
	m.onFileDone = onDone
	m.fileMu.Unlock()
}

func newFileID() (string, error) {
	raw := make([]byte, fileIDBytes)
	if _, err := io.ReadFull(cryptoRand.Reader, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// SendFile starts sending a local file to the connected peer. It returns
// immediately after the offer is sent; chunks stream in the background.
func (m *Manager) SendFile(path string) (*FileOfferInfo, error) {
	if !m.IsConnected() {
		return nil, fmt.Errorf("not connected to a peer")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.IsDir() {
		f.Close()
		return nil, fmt.Errorf("cannot send a directory")
	}

	id, err := newFileID()
	if err != nil {
		f.Close()
		return nil, err
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}

	totalChunks := int(stat.Size())/fileChunkSize + 1
	name := unsafeFilename.ReplaceAllString(filepath.Base(path), "")

	offer := &FileOfferInfo{
		FileID:      id,
		Name:        name,
		Size:        stat.Size(),
		TotalChunks: totalChunks,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}

	offerJSON, err := json.Marshal(offer)
	if err != nil {
		f.Close()
		return nil, err
	}

	if err := m.writeMessage("file_offer", offerJSON); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to send file offer: %w", err)
	}

	state := &fileSendState{info: offer, file: f, path: path}

	m.fileMu.Lock()
	if m.outgoing != nil {
		m.fileMu.Unlock()
		f.Close()
		return nil, fmt.Errorf("another file is already being sent")
	}
	m.outgoing = state
	m.fileMu.Unlock()

	go m.sendChunks(state)

	return offer, nil
}

// sendChunks streams the file to the peer. Each chunk is separated by a
// small pause so the peer's chat messages interleave with file chunks
// and chat is never blocked by a large transfer.
func (m *Manager) sendChunks(state *fileSendState) {
	ticker := time.NewTicker(fileChunkPause)
	defer ticker.Stop()

	idBytes, err := hex.DecodeString(state.info.FileID)
	if err != nil {
		m.failOutgoing(state, "internal error: invalid file id")
		return
	}

	buf := make([]byte, fileChunkSize)

	for {
		select {
		case <-ticker.C:
		case <-m.ctx.Done():
			return
		}

		if !m.IsConnected() {
			m.failOutgoing(state, "connection closed during transfer")
			return
		}

		n, readErr := state.file.Read(buf)

		if n == 0 && readErr != nil && readErr != io.EOF {
			m.failOutgoing(state, fmt.Sprintf("failed to read file: %v", readErr))
			return
		}

		if n > 0 {
			payload := make([]byte, 0, len(idBytes)+n)
			payload = append(payload, idBytes...)
			payload = append(payload, buf[:n]...)

			if err := m.writeMessage("file_chunk", payload); err != nil {
				m.failOutgoing(state, fmt.Sprintf("sending file failed: %v", err))
				return
			}
			state.sent += int64(n)

			if state.sent-state.lastNotified >= fileReportStep {
				state.lastNotified = state.sent
				m.notifyProgress(FileProgress{
					FileID:   state.info.FileID,
					Name:     state.info.Name,
					Sent:     state.sent,
					Total:    state.info.Size,
					Outgoing: true,
				})
			}
		}

		// Completion: EOF (n == 0) or we've streamed the announced size.
		done := n == 0 || state.sent >= state.info.Size
		if !done {
			continue
		}

		if err := m.writeMessage("file_end", idBytes); err != nil {
			m.failOutgoing(state, fmt.Sprintf("sending file failed: %v", err))
			return
		}

		state.file.Close()
		m.notifyProgress(FileProgress{
			FileID:   state.info.FileID,
			Name:     state.info.Name,
			Sent:     state.sent,
			Total:    state.info.Size,
			Outgoing: true,
		})
		m.notifyResult(FileResult{
			FileID:   state.info.FileID,
			Name:     state.info.Name,
			Outgoing: true,
			OK:       true,
		})

		m.fileMu.Lock()
		if m.outgoing == state {
			m.outgoing = nil
		}
		m.fileMu.Unlock()
		return
	}
}

func (m *Manager) failOutgoing(state *fileSendState, reason string) {
	state.file.Close()
	m.fileMu.Lock()
	if m.outgoing == state {
		m.outgoing = nil
	}
	m.fileMu.Unlock()

	m.notifyResult(FileResult{
		FileID:   state.info.FileID,
		Name:     state.info.Name,
		Outgoing: true,
		OK:       false,
		Message:  reason,
	})
}

// ---- Receiver side ----

func (m *Manager) handleFileOffer(content []byte) {
	var offer FileOfferInfo
	if err := json.Unmarshal(content, &offer); err != nil {
		return
	}
	if offer.TotalChunks <= 0 || len(offer.FileID) < 8 {
		return
	}

	dir := filepath.Join(m.dataDir, "incoming")
	if err := os.MkdirAll(dir, 0700); err != nil {
		m.notifyResult(FileResult{
			FileID: offer.FileID, Name: offer.Name,
			Outgoing: false, OK: false,
			Message: "cannot create incoming folder: " + err.Error(),
		})
		return
	}

	spoolPath := filepath.Join(dir, fmt.Sprintf("%s_%s", offer.FileID[:8], offer.Name))
	out, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		m.notifyResult(FileResult{
			FileID: offer.FileID, Name: offer.Name,
			Outgoing: false, OK: false,
			Message: "cannot open spool file: " + err.Error(),
		})
		return
	}

	state := &fileRecvState{info: &offer, out: out, spoolPath: spoolPath}

	m.fileMu.Lock()
	m.incoming[offer.FileID] = state
	cb := m.onFileOffer
	m.fileMu.Unlock()

	if cb != nil {
		cb(offer)
	}
}

func (m *Manager) handleFileChunk(content []byte) {
	if len(content) <= fileIDBytes {
		return
	}

	id := hex.EncodeToString(content[:fileIDBytes])
	data := content[fileIDBytes:]

	m.fileMu.Lock()
	state, ok := m.incoming[id]
	m.fileMu.Unlock()
	if !ok {
		return // unknown file (aborted earlier?)
	}

	if int64(len(data)) > state.info.Size-state.received {
		_ = m.failIncoming(id, state, "received more data than announced")
		return
	}

	if len(data) > 0 {
		if _, err := state.out.Write(data); err != nil {
			_ = m.failIncoming(id, state, "failed to write chunk: "+err.Error())
			return
		}
		state.received += int64(len(data))

		if state.received-state.lastNotified >= fileReportStep {
			state.lastNotified = state.received
			m.notifyProgress(FileProgress{
				FileID:   state.info.FileID,
				Name:     state.info.Name,
				Sent:     state.received,
				Total:    state.info.Size,
				Outgoing: false,
			})
		}
	}
}

func (m *Manager) handleFileEnd(content []byte) {
	if len(content) < fileIDBytes {
		return
	}

	id := hex.EncodeToString(content[:fileIDBytes])

	m.fileMu.Lock()
	state, ok := m.incoming[id]
	delete(m.incoming, id)
	m.fileMu.Unlock()
	if !ok {
		return
	}

	if err := state.out.Close(); err != nil {
		_ = m.failIncoming(id, state, "failed to close spool file: "+err.Error())
		return
	}

	// Verify integrity against the hash announced in the signed offer.
	hasher := sha256.New()
	f, err := os.Open(state.spoolPath)
	if err == nil {
		_, err = io.Copy(hasher, f)
		f.Close()
	}
	if err != nil {
		_ = m.failIncoming(id, state, "failed to read received file: "+err.Error())
		return
	}
	if hex.EncodeToString(hasher.Sum(nil)) != state.info.SHA256 {
		_ = m.failIncoming(id, state, "SHA-256 mismatch — file corrupted or tampered")
		return
	}

	m.notifyProgress(FileProgress{
		FileID:   state.info.FileID,
		Name:     state.info.Name,
		Sent:     state.received,
		Total:    state.info.Size,
		Outgoing: false,
	})
	m.notifyResult(FileResult{
		FileID:   state.info.FileID,
		Name:     state.info.Name,
		SavePath: state.spoolPath,
		Outgoing: false,
		OK:       true,
	})
}

func (m *Manager) failIncoming(id string, state *fileRecvState, reason string) bool {
	m.fileMu.Lock()
	_, exists := m.incoming[id]
	delete(m.incoming, id)
	m.fileMu.Unlock()
	if !exists {
		return false
	}

	state.out.Close()
	os.Remove(state.spoolPath)

	m.notifyResult(FileResult{
		FileID:   state.info.FileID,
		Name:     state.info.Name,
		Outgoing: false,
		OK:       false,
		Message:  reason,
	})
	return true
}

// abortTransfers cancels any in-flight transfers (called when the peer
// disconnects while a transfer is running).
func (m *Manager) abortTransfers(reason string) {
	m.fileMu.Lock()
	out := m.outgoing
	m.outgoing = nil
	incoming := m.incoming
	m.incoming = make(map[string]*fileRecvState)
	m.fileMu.Unlock()

	if out != nil {
		out.file.Close()
		m.notifyResult(FileResult{
			FileID:   out.info.FileID,
			Name:     out.info.Name,
			Outgoing: true,
			OK:       false,
			Message:  reason,
		})
	}

	for _, state := range incoming {
		state.out.Close()
		os.Remove(state.spoolPath)
		m.notifyResult(FileResult{
			FileID:   state.info.FileID,
			Name:     state.info.Name,
			Outgoing: false,
			OK:       false,
			Message:  reason,
		})
	}
}

func (m *Manager) notifyProgress(p FileProgress) {
	m.fileMu.Lock()
	cb := m.onFileProgress
	m.fileMu.Unlock()
	if cb != nil {
		cb(p)
	}
}

func (m *Manager) notifyResult(r FileResult) {
	m.fileMu.Lock()
	cb := m.onFileDone
	m.fileMu.Unlock()
	if cb != nil {
		cb(r)
	}
}
