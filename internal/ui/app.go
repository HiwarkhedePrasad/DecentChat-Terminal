package ui

import (
	"fmt"
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
	mode string // "normal", "connecting", "chat", "waiting"

	// For incoming connections
	pendingPeerID   string

	// Poll ticker
	lastPollTime    time.Time
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
)

// Messages for tea
type (
	statusMsg      string
	errorMsg       string
	peerMsg        string
	connectedMsg   struct{ peerID string }
	disconnectedMsg struct{}
	incomingMsg    struct{ peerID string }
	pollMsg        struct{}
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
	}

	p := tea.NewProgram(m, tea.WithAltScreen())

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
			if m.mode == "waiting" {
				m.mode = "normal"
				m.messages = append(m.messages, systemStyle.Render("[System] Cancelled"))
			}
		default:
			m.input += string(msg.Runes)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

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
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

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
		b.WriteString(m.messages[i])
		b.WriteString("\n")
	}

	// Fill remaining space
	for i := len(m.messages) - start; i < visibleLines; i++ {
		b.WriteString("\n")
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

	return sb.String()
}

func (m Model) renderCommands() string {
	if m.mode == "chat" {
		return commandStyle.Render("Type message to send | /disconnect | /exit")
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

	// Handle commands
	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}

	// If in chat mode, send message
	if m.mode == "chat" && m.connectedPeer != "" {
		if err := m.networkManager.SendMessage(input); err != nil {
			m.messages = append(m.messages, errorStyle.Render("[Error] ")+err.Error())
		} else {
			m.messages = append(m.messages, successStyle.Render("[You] ")+input)
		}
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
