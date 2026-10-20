package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"decentchat/internal/identity"
	"decentchat/internal/network"
	"decentchat/internal/signaling"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model represents the application state
type Model struct {
	identity        *identity.Identity
	signalingClient *signaling.Client
	networkManager  *network.Manager
	tunnelURL       string
	version         string

	// UI State
	width           int
	height          int
	input           string
	messages        []string
	status          string
	connectedPeer   string
	ready           bool

	// Modes
	mode string // "normal", "connecting", "chat", "waiting", "saveas"

	// For incoming connections
	pendingPeerID string

	// Poll ticker
	lastPollTime time.Time

	// File transfer state
	fileDropTags    map[string]string              // staged send: tag ("FILE1") -> local path
	receivedFiles   map[string]network.FileResult  // fileID -> received file info (clickable)
	fileTags        map[string]string              // fileID -> display tag ("FILEn")
	activeTransfers map[string]string              // fileID -> one-line progress text
	savedPaths      map[string]string              // fileID -> user-chosen save destination
	fileMsgLines    map[int]string                 // message index -> fileID (clickable lines)
	rowToMsg        *map[int]int                   // screen row -> message index (built while rendering)
	fileCounter     int
	fileMenuID      string // received-file menu is open for this fileID ("", closed)
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00FF00")).
			MarginBottom(1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFF00"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00"))

	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	peerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00BFFF"))

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	commandStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	fileMenuStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFB000"))
)

// Messages for tea
type (
	statusMsg       string
	errorMsg        string
	peerMsg         string
	connectedMsg    struct{ peerID string }
	disconnectedMsg struct{}
	incomingMsg     struct{ peerID string }
	pollMsg         struct{}

	// File transfer messages
	fileOfferMsg    struct{ offer network.FileOfferInfo }
	fileProgressMsg struct{ update network.FileProgress }
	fileDoneMsg     struct{ result network.FileResult }
)

// NewApp creates a new application
func NewApp(id *identity.Identity, sigClient *signaling.Client, networkMgr *network.Manager, localTunnel string, version string) *tea.Program {
	m := Model{
		identity:        id,
		signalingClient: sigClient,
		networkManager:  networkMgr,
		tunnelURL:       localTunnel,
		version:         version,
		messages:        make([]string, 0),
		status:          "Online",
		mode:            "normal",
		fileDropTags:    make(map[string]string),
		receivedFiles:   make(map[string]network.FileResult),
		fileTags:        make(map[string]string),
		activeTransfers: make(map[string]string),
		savedPaths:      make(map[string]string),
		fileMsgLines:    make(map[int]string),
	}
	rowToMsg := make(map[int]int)
	m.rowToMsg = &rowToMsg

	// Enable mouse events so received files can be clicked for save options
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Set up Network callbacks to send messages to the bubbletea program
	networkMgr.SetCallbacks(
		func(msg string) {
			p.Send(peerMsg(msg))
		},
		func() {
			p.Send(connectedMsg{})
		},
		func() {
			p.Send(disconnectedMsg{})
		},
		func(peerID string) {
			p.Send(incomingMsg{peerID: peerID})
		},
	)

	// File-transfer callbacks
	networkMgr.SetFileCallbacks(
		func(offer network.FileOfferInfo) {
			p.Send(fileOfferMsg{offer: offer})
		},
		func(update network.FileProgress) {
			p.Send(fileProgressMsg{update: update})
		},
		func(result network.FileResult) {
			p.Send(fileDoneMsg{result: result})
		},
	)

	return p
}

// Init initializes the application
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		registerUser(m.signalingClient),
		pollForUpdates(m.signalingClient, m.identity.UserID),
	)
}

// Update handles updates
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Received-file menu is open: s = Save As, o = open folder.
		// Any other key dismisses the menu and is processed normally.
		if m.fileMenuID != "" && m.mode != "saveas" {
			switch strings.ToLower(msg.String()) {
			case "s":
				if entry, ok := m.receivedFiles[m.fileMenuID]; ok {
					m.mode = "saveas"
					m.input = m.defaultSavePath(entry.Name)
				}
				return m, nil
			case "o":
				m.openReceivedFolder(m.fileMenuID)
				return m, nil
			case "esc":
				m.fileMenuID = ""
				return m, nil
			default:
				m.fileMenuID = ""
			}
		}

		switch msg.Type {
		case tea.KeyEnter:
			return m.handleInput()
		case tea.KeyCtrlC, tea.KeyCtrlD:
			m.signalingClient.SetOffline(m.identity.UserID)
			return m, tea.Quit
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case tea.KeyEsc:
			if m.mode == "saveas" {
				// Cancel save but return to the file menu
				m.mode = m.restoreMode()
				m.input = ""
				return m, nil
			}
			if m.fileMenuID != "" {
				m.fileMenuID = ""
				return m, nil
			}
			if m.mode == "waiting" {
				m.mode = "normal"
				m.messages = append(m.messages, systemStyle.Render("[System] Cancelled"))
			}
		default:
			m.input += string(msg.Runes)
			// Catch dropped/pasted file paths and replace them with [FILEn]
			m.captureFilePaths()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.handleFileClick(msg.Y)
		}

	case statusMsg:
		m.messages = append(m.messages, systemStyle.Render("[System] ")+string(msg))

	case errorMsg:
		// Unwrap the error message string to see the full cause
		errMsg := string(msg)
		if len(errMsg) > 100 {
			// Bubbletea sometimes truncates long lines, let's wrap or format it
			errMsg = strings.ReplaceAll(errMsg, ": dial tcp:", ": \n  dial tcp:")
		}
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+errMsg)

	case peerMsg:
		m.messages = append(m.messages, peerStyle.Render("[Peer] ")+string(msg))

	case connectedMsg:
		m.mode = "chat"
		if msg.peerID != "" {
			m.connectedPeer = msg.peerID
		}
		m.messages = append(m.messages, successStyle.Render("✓ Connected to peer: ")+m.connectedPeer)

	case disconnectedMsg:
		m.mode = "normal"
		m.connectedPeer = ""
		m.pendingPeerID = ""
		m.messages = append(m.messages, errorStyle.Render("✗ Disconnected from peer"))

	case incomingMsg:
		// Incoming WebSocket connection hit our local server
		if m.mode == "normal" && m.connectedPeer == "" {
			m.pendingPeerID = msg.peerID
			m.mode = "waiting"
			shortID := msg.peerID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			m.messages = append(m.messages,
				systemStyle.Render(fmt.Sprintf("[System] Incoming connection from: %s", shortID)),
				systemStyle.Render("[System] Type /accept to accept or /decline to decline"),
			)
		}

	case pollMsg:
		// Periodic polling for offers/answers
		return m, tea.Batch(
			checkForOffers(&m),
			pollForUpdates(m.signalingClient, m.identity.UserID),
		)

	case fileOfferMsg:
		tag := m.tagForFile(msg.offer.FileID)
		m.activeTransfers[msg.offer.FileID] = fmt.Sprintf("Receiving %s 0%%", tag)

	case fileProgressMsg:
		tag := m.tagForFile(msg.update.FileID)
		direction := "Receiving"
		if msg.update.Outgoing {
			direction = "Sending"
		}
		pct := 0
		if msg.update.Total > 0 {
			pct = int(100 * msg.update.Sent / msg.update.Total)
		}
		if msg.update.Sent >= msg.update.Total {
			pct = 100
		}
		kb := msg.update.Sent / 1024
		totalKB := msg.update.Total / 1024
		m.activeTransfers[msg.update.FileID] = fmt.Sprintf("%s %s %d/%d KB (%d%%)", direction, tag, kb, totalKB, pct)

	case fileDoneMsg:
		r := msg.result
		delete(m.activeTransfers, r.FileID)

		if r.Outgoing {
			tag := m.tagForFile(r.FileID)
			if r.OK {
				m.messages = append(m.messages, successStyle.Render(fmt.Sprintf("[You] ✓ File sent %s", tag)))
				delete(m.fileDropTags, tag)
			} else {
				m.messages = append(m.messages, errorStyle.Render(fmt.Sprintf("[Error] Sending %s failed: %s", tag, r.Message)))
			}
		} else {
			tag := m.tagForFile(r.FileID)
			if r.OK {
				m.receivedFiles[r.FileID] = r
				idx := len(m.messages)
				m.messages = append(m.messages, successStyle.Render(
					fmt.Sprintf("📄 [%s] File %s received — click this line to save it", tag, r.Name)))
				m.fileMsgLines[idx] = r.FileID
			} else {
				m.messages = append(m.messages, errorStyle.Render(fmt.Sprintf("[Error] Receiving %s failed: %s", tag, r.Message)))
				delete(m.receivedFiles, r.FileID)
				delete(m.savedPaths, r.FileID)
			}
		}
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	// Rebuild the screen-row -> message-index map for mouse clicks
	rowToMsg := make(map[int]int)
	*m.rowToMsg = rowToMsg
	row := 0

	var b strings.Builder

	// Header
	header := m.renderHeader()
	b.WriteString(header)
	b.WriteString("\n\n")

	// Commands
	commands := m.renderCommands()
	b.WriteString(commands)
	b.WriteString("\n\n")

	// Messages area
	b.WriteString("─── Chat ───\n")
	visibleLines := m.height - 12
	if visibleLines < 5 {
		visibleLines = 5
	}

	start := 0
	if len(m.messages) > visibleLines {
		start = len(m.messages) - visibleLines
	}

	for i := start; i < len(m.messages); i++ {
		rowToMsg[row] = i
		b.WriteString(m.messages[i])
		b.WriteString("\n")
		row += 1 + strings.Count(m.messages[i], "\n")
	}

	// Fill remaining space
	for i := len(m.messages) - start; i < visibleLines; i++ {
		b.WriteString("\n")
	}

	// Received-file menu bar
	if m.fileMenuID != "" {
		if tag, ok := m.fileTags[m.fileMenuID]; ok {
			b.WriteString(fileMenuStyle.Render(
				fmt.Sprintf("  [%s] (s) Save As…   (o) Open Folder   (Esc) dismiss", tag)))
			b.WriteString("\n\n")
		}
	}

	// Input area
	b.WriteString("\n")
	b.WriteString(m.renderInput())

	return b.String()
}

func (m Model) renderHeader() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render(fmt.Sprintf("DecentChat v%s", m.version)))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("User ID: %s", m.identity.ShortID()))

	if m.connectedPeer != "" {
		sb.WriteString(" │ ")
		sb.WriteString(successStyle.Render(fmt.Sprintf("Connected: %s", m.connectedPeer)))
	} else if m.mode == "waiting" && m.pendingPeerID != "" {
		sb.WriteString(" │ ")
		sb.WriteString(statusStyle.Render("Incoming connection..."))
	} else if m.mode == "connecting" {
		sb.WriteString(" │ ")
		sb.WriteString(statusStyle.Render("Connecting..."))
	} else {
		sb.WriteString(" │ ")
		sb.WriteString(fmt.Sprintf("Status: %s", m.status))
	}

	// Live file-transfer progress (chat stays usable while files flow)
	if len(m.activeTransfers) > 0 {
		texts := make([]string, 0, len(m.activeTransfers))
		for _, t := range m.activeTransfers {
			texts = append(texts, t)
		}
		sb.WriteString(" │ ")
		sb.WriteString(statusStyle.Render(strings.Join(texts, " · ")))
	}

	return sb.String()
}

func (m Model) renderCommands() string {
	if m.mode == "saveas" {
		return commandStyle.Render("Enter to save · Esc to cancel")
	}
	if m.mode == "chat" {
		return commandStyle.Render("Type message | Drop file to send | /disconnect | /exit")
	}
	if m.mode == "waiting" {
		return commandStyle.Render("/accept | /decline")
	}
	return commandStyle.Render("/list | /connect <id> | /status | /exit")
}

func (m Model) renderInput() string {
	var prompt string
	switch m.mode {
	case "chat":
		prompt = "[chat] "
	case "waiting":
		prompt = "[waiting] "
	case "connecting":
		prompt = "[connecting] "
	case "saveas":
		prompt = "[save-as] "
	default:
		prompt = ""
	}
	return prompt + "> " + m.input + "█"
}

func (m *Model) handleInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input)
	m.input = ""

	if input == "" {
		return m, nil
	}

	// Save-As prompt takes over the input line while active
	if m.mode == "saveas" {
		m.completeSaveAs(input)
		return m, nil
	}

	// Handle commands
	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}

	// Staged files ([FILEn]) on the input line?
	if fileTagRe.MatchString(input) {
		m.sendInputWithFiles(input)
		return m, nil
	}

	// If in chat mode, send message
	if m.mode == "chat" && m.connectedPeer != "" {
		m.sendChat(input)
		return m, nil
	}

	m.messages = append(m.messages, systemStyle.Render("[System] Unknown command. Type /help for available commands."))
	return m, nil
}

func (m *Model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	command := parts[0]
	args := parts[1:]

	switch command {
	case "/list":
		return m.listUsers()

	case "/connect":
		if len(args) < 1 {
			m.messages = append(m.messages, systemStyle.Render("[System] Usage: /connect <user_id>"))
			return m, nil
		}
		return m.connectToPeer(args[0])

	case "/disconnect":
		return m.disconnect()

	case "/accept":
		return m.acceptConnection()

	case "/decline":
		return m.declineConnection()

	case "/exit", "/quit":
		m.signalingClient.SetOffline(m.identity.UserID)
		return m, tea.Quit

	case "/help":
		m.messages = append(m.messages, systemStyle.Render("[System] Available commands:"))
		m.messages = append(m.messages, "  /list          - Show online users")
		m.messages = append(m.messages, "  /connect <id>  - Connect to a peer")
		m.messages = append(m.messages, "  /disconnect    - End current connection")
		m.messages = append(m.messages, "  /accept        - Accept incoming connection")
		m.messages = append(m.messages, "  /decline       - Decline incoming connection")
		m.messages = append(m.messages, "  /exit          - Quit the application")
		m.messages = append(m.messages, "  Files: drop a file into the terminal — it shows as [FILEn];")
		m.messages = append(m.messages, "  press Enter to send it. Click a received 📄 line to save it.")
		return m, nil

	case "/status":
		if m.connectedPeer != "" {
			m.messages = append(m.messages, systemStyle.Render(fmt.Sprintf("[System] Connected to: %s", m.connectedPeer)))
		} else {
			m.messages = append(m.messages, systemStyle.Render("[System] Not connected to any peer"))
		}
		return m, nil

	case "/clear":
		m.messages = make([]string, 0)
		m.fileMsgLines = make(map[int]string)
		return m, nil

	default:
		m.messages = append(m.messages, errorStyle.Render(fmt.Sprintf("[System] Unknown command: %s", command)))
		return m, nil
	}
}

func (m *Model) listUsers() (tea.Model, tea.Cmd) {
	users, err := m.signalingClient.GetOnlineUsers()
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		return m, nil
	}

	m.messages = append(m.messages, systemStyle.Render("[System] Online users:"))
	count := 0
	for _, user := range users {
		if user.UserID != m.identity.UserID {
			shortID := user.UserID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			m.messages = append(m.messages, fmt.Sprintf("  • %s", shortID))
			count++
		}
	}

	if count == 0 {
		m.messages = append(m.messages, systemStyle.Render("  (no other users online)"))
	}

	return m, nil
}

func (m *Model) connectToPeer(peerID string) (tea.Model, tea.Cmd) {
	if m.connectedPeer != "" {
		m.messages = append(m.messages, systemStyle.Render("[System] Already connected. Use /disconnect first."))
		return m, nil
	}

	// Get peer's full ID (might be short ID)
	users, err := m.signalingClient.GetOnlineUsers()
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		return m, nil
	}

	var targetUser *signaling.User
	for _, user := range users {
		if strings.HasPrefix(user.UserID, peerID) {
			targetUser = &user
			break
		}
	}

	if targetUser == nil {
		m.messages = append(m.messages, errorStyle.Render(fmt.Sprintf("[Error] User %s not found or offline", peerID)))
		return m, nil
	}

	m.messages = append(m.messages, systemStyle.Render(fmt.Sprintf("[System] Connecting to %s...", peerID)))
	m.mode = "connecting"

	// Connect to peer's tunnel
	err = m.networkManager.ConnectToPeer(targetUser.UserID, targetUser.TunnelURL, targetUser.PublicEncKey, targetUser.PublicIdentityKey)
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		m.mode = "normal"
		return m, nil
	}

	m.connectedPeer = peerID
	m.messages = append(m.messages, systemStyle.Render("[System] Dialed peer. Waiting for them to accept..."))

	return m, nil
}

func (m *Model) disconnect() (tea.Model, tea.Cmd) {
	if m.connectedPeer == "" && m.mode == "normal" {
		m.messages = append(m.messages, systemStyle.Render("[System] Not connected to any peer."))
		return m, nil
	}

	m.networkManager.CloseConnection()

	m.messages = append(m.messages, systemStyle.Render(fmt.Sprintf("[System] Disconnected")))
	m.connectedPeer = ""
	m.mode = "normal"
	m.pendingPeerID = ""

	return m, nil
}

func (m *Model) acceptConnection() (tea.Model, tea.Cmd) {
	if m.pendingPeerID == "" {
		m.messages = append(m.messages, systemStyle.Render("[System] No pending connection request."))
		return m, nil
	}

	peerID := m.pendingPeerID
	if len(peerID) > 8 {
		peerID = peerID[:8]
	}

	m.messages = append(m.messages, systemStyle.Render(fmt.Sprintf("[System] Accepting connection from %s...", peerID)))

	// Fetch peer keys from signaling server since we only got their ID
	peerUser, err := m.signalingClient.GetUser(m.pendingPeerID)
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] Failed to fetch peer keys: ")+err.Error())
		m.mode = "normal"
		m.pendingPeerID = ""
		m.networkManager.DeclineConnection()
		return m, nil
	}

	// Accept the connection and establish secure shared secret
	err = m.networkManager.AcceptConnection(peerUser.PublicEncKey, peerUser.PublicIdentityKey)
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		m.mode = "normal"
		m.pendingPeerID = ""
		return m, nil
	}

	m.connectedPeer = peerID
	m.mode = "chat"
	m.pendingPeerID = ""
	m.messages = append(m.messages, systemStyle.Render("[System] Connection accepted. You can now chat!"))

	return m, nil
}

func (m *Model) declineConnection() (tea.Model, tea.Cmd) {
	if m.pendingPeerID == "" {
		m.messages = append(m.messages, systemStyle.Render("[System] No pending connection request."))
		return m, nil
	}

	m.messages = append(m.messages, systemStyle.Render("[System] Connection declined"))
	m.networkManager.DeclineConnection()
	m.mode = "normal"
	m.pendingPeerID = ""

	return m, nil
}

// ---- File drop capture & sending ----

var (
	winQuotedRe  = regexp.MustCompile(`"([A-Za-z]:\\[^"\r\n\v]+)"`)
	winPathRe    = regexp.MustCompile(`[A-Za-z]:\\[^\s"]+`)
	unixQuotedRe = regexp.MustCompile(`"(/[^"\r\n]+)"`)
	unixPathRe   = regexp.MustCompile(`/[^\s"]{2,}`)
	fileTagRe    = regexp.MustCompile(`\[FILE(\d+)\]`)
)

// captureFilePaths scans the input line for pasted/dropped file paths and
// replaces them with [FILEn] placeholders. Real paths staged for sending;
// ordinary text (where the "path" doesn't exist locally) is left alone.
func (m *Model) captureFilePaths() {
	mutate := func(re *regexp.Regexp) {
		m.input = re.ReplaceAllStringFunc(m.input, func(match string) string {
			candidate := strings.Trim(match, `"`+" ")
			if tag := m.stageFile(candidate); tag != "" {
				return tag
			}
			return match
		})
	}

	// Quoted paths first (handles paths containing spaces)
	mutate(winQuotedRe)
	mutate(unixQuotedRe)
	// Unquoted paths
	mutate(winPathRe)
	mutate(unixPathRe)
}

// stageFile adds or re-uses a placeholder tag if raw points to an existing
// local file. Returns "" when raw is not a real file.
func (m *Model) stageFile(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}

	// Re-use the same placeholder if this file is already staged
	for tag, existing := range m.fileDropTags {
		if existing == p {
			return "[" + tag + "]"
		}
	}

	m.fileCounter++
	tag := fmt.Sprintf("FILE%d", m.fileCounter)
	m.fileDropTags[tag] = p
	return "[" + tag + "]"
}

// sendInputWithFiles splits an input line into chat text and staged files,
// sends the text as a chat message, and transfers each staged file.
func (m *Model) sendInputWithFiles(input string) {
	if m.connectedPeer == "" {
		m.messages = append(m.messages,
			systemStyle.Render("[System] Not connected — files stay staged. Use /connect first."))
		return
	}

	tags := fileTagRe.FindAllString(input, -1)
	remainder := strings.TrimSpace(
		strings.Join(strings.Fields(fileTagRe.ReplaceAllString(input, "")), " "))

	if remainder != "" {
		m.sendChat(remainder)
	}

	for _, tag := range tags {
		m.sendStagedFile(tag)
	}
}

func (m *Model) sendChat(text string) {
	if err := m.networkManager.SendMessage(text); err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
	} else {
		m.messages = append(m.messages, successStyle.Render("[You] ")+text)
	}
}

func (m *Model) sendStagedFile(tag string) {
	key := strings.Trim(tag, "[]")
	path, ok := m.fileDropTags[key]
	if !ok {
		m.messages = append(m.messages,
			systemStyle.Render(fmt.Sprintf("[System] %s is not a staged file", tag)))
		return
	}

	offer, err := m.networkManager.SendFile(path)
	if err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		return
	}

	m.fileTags[offer.FileID] = key
	m.activeTransfers[offer.FileID] = fmt.Sprintf("Sending %s 0%%", key)
	m.messages = append(m.messages, successStyle.Render(fmt.Sprintf("[You] Sending %s…", tag)))
}

// tagForFile returns (creating if needed) the display tag for a file ID.
func (m *Model) tagForFile(fileID string) string {
	if tag, ok := m.fileTags[fileID]; ok {
		return tag
	}
	m.fileCounter++
	tag := fmt.Sprintf("FILE%d", m.fileCounter)
	m.fileTags[fileID] = tag
	return tag
}

// ---- Received-file click menu ----

// handleFileClick maps a screen row to a message line and opens the file
// menu when the line is a received file.
func (m *Model) handleFileClick(row int) {
	if m.mode == "saveas" {
		return
	}

	idx, ok := (*m.rowToMsg)[row]
	if !ok {
		// Clicking anywhere outside a message line dismisses the menu
		if m.fileMenuID != "" {
			m.fileMenuID = ""
		}
		return
	}

	if fileID, ok := m.fileMsgLines[idx]; ok {
		m.fileMenuID = fileID
	} else {
		m.fileMenuID = ""
	}
}

// defaultSavePath suggests a sensible destination under ~/Downloads/DecentChat
func (m *Model) defaultSavePath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return name
	}
	dir := filepath.Join(home, "Downloads", "DecentChat")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, name)
}

// completeSaveAs copies the received (spooled) file to the user-chosen path.
func (m *Model) completeSaveAs(destInput string) {
	m.mode = m.restoreMode()
	m.input = ""

	id := m.fileMenuID
	entry, ok := m.receivedFiles[id]
	if !ok {
		m.messages = append(m.messages, systemStyle.Render("[System] File no longer available."))
		m.fileMenuID = ""
		return
	}

	dest := expandPath(destInput)
	if dest == "" {
		// Back to the menu, empty input just cancels completely
		m.fileMenuID = ""
		m.messages = append(m.messages, systemStyle.Render("[System] Save cancelled."))
		return
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] Cannot create folder: ")+err.Error())
		return
	}
	if err := copyFile(entry.SavePath, dest); err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] Save failed: ")+err.Error())
		return
	}

	m.savedPaths[id] = dest
	m.fileMenuID = ""
	m.messages = append(m.messages,
		successStyle.Render(fmt.Sprintf("[System] ✓ Saved %s to %s", m.tagForFile(id), dest)))
}

// openReceivedFolder opens the folder containing the saved (or spooled) file.
func (m *Model) openReceivedFolder(fileID string) {
	var dir string
	if dest, ok := m.savedPaths[fileID]; ok {
		dir = filepath.Dir(dest)
	} else if entry, ok := m.receivedFiles[fileID]; ok {
		dir = filepath.Dir(entry.SavePath)
	} else {
		return
	}

	if err := openFolder(dir); err != nil {
		m.messages = append(m.messages, errorStyle.Render("[Error] Cannot open folder: ")+err.Error())
	} else {
		m.messages = append(m.messages, systemStyle.Render("[System] Opened folder: ")+dir)
	}
}

// restoreMode returns the mode that should follow a save-as action.
func (m Model) restoreMode() string {
	if m.connectedPeer != "" {
		return "chat"
	}
	return "normal"
}

// ---- helpers ----

// expandPath expands a leading ~ to the user's home directory.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	return p
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// openFolder opens the given folder in the OS file manager.
func openFolder(dir string) error {
	switch runtime.GOOS {
	case "windows":
		// Start (not Run) so an odd explorer exit code doesn't surface as an error
		return exec.Command("explorer", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// Commands

func registerUser(client *signaling.Client) tea.Cmd {
	return func() tea.Msg {
		if err := client.Register(); err != nil {
			return errorMsg("Failed to register: " + err.Error())
		}
		return statusMsg("Registered with signaling server")
	}
}

func pollForUpdates(client *signaling.Client, userID string) tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return pollMsg{}
	})
}

func checkForOffers(m *Model) tea.Cmd {
	return func() tea.Msg {
		// Since WebSocket connections directly trigger the incoming webhook on the server,
		// there are no "offers" to poll from the signaling server anymore.
		// However, we still need to check if the peer we dialed has accepted and marked themselves
		// "chatting" or dropped... wait, no we don't.
		// If we are "connecting", we are just waiting for the peer to accept the WebSocket connection on their end.
		// So this function is largely a no-op now, except maybe to check if the peer drops offline.

		return nil
	}
}
