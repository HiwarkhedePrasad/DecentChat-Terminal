# DecentChat

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/dl/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](#license)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#contributing)
[![Security](https://img.shields.io/badge/Security-E2E%20Encrypted-green.svg)](#security)

**Semi-Decentralized Encrypted Terminal Chat**

A privacy-first, terminal-based peer-to-peer messenger with end-to-end encryption. DecentChat enables secure, direct communication between users without relying on a central server for message storage or relay.

---

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Security](#security)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Usage](#usage)
- [Technical Details](#technical-details)
- [Project Structure](#project-structure)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## Overview

DecentChat is a CLI application designed for users who prioritize privacy and security in their communications. Unlike traditional messaging applications that rely on centralized servers to store and relay messages, DecentChat uses a semi-decentralized architecture where the central server (Supabase) is only used for signaling—publishing presence and tunnel addresses so peers can find each other. Once connected, all communication flows directly between users through a Cloudflare Tunnel, encrypted at the application level.

### Why DecentChat?

| Traditional Messengers | DecentChat |
|----------------------|------------|
| Messages stored on servers | Messages never touch a server |
| Trust server with encryption | End-to-end encryption you control |
| Central point of failure | Direct P2P communication |
| Account required | No accounts, just cryptographic identity |
| Metadata collection | Minimal metadata, privacy-first |

---

## Features

### End-to-End Encryption

DecentChat implements a multi-layer encryption system that ensures your communications remain private and secure. The cryptographic architecture combines multiple industry-standard algorithms:

- **X25519 Key Exchange**: Elliptic-curve Diffie-Hellman key exchange to derive a shared secret between the two peers from their published public keys.
- **AES-256-GCM Encryption**: Authenticated symmetric encryption for message confidentiality and integrity. A unique random nonce is generated for every message.
- **Ed25519 Signing**: Every message is digitally signed. Recipients verify the signature against the sender's published identity key before accepting a message, preventing forgery.

### Semi-Decentralized Architecture

- **Signaling Only**: Supabase is used exclusively for presence and connection establishment. Your user row contains only your user ID, public keys, and your (ephemeral) tunnel URL—never your messages or private keys.
- **No Message Storage**: Your messages are never stored on any server. They flow directly between peers through the encrypted tunnel, leaving no trace on intermediate infrastructure.
- **Direct P2P**: After the handshake, all communication flows over a single direct WebSocket connection between the two peers.
- **Cloudflare Quick Tunnel Integration**: DecentChat automatically spawns a `cloudflared` quick tunnel, providing NAT traversal without opening ports or exposing your local network.

### Privacy-First Design

- **Local Key Generation**: All cryptographic keys are generated and stored locally on your device. Only public keys are ever published to the signaling server.
- **Private Keys Never Leave Device**: Your private keys are never transmitted over the network.
- **No Account Required**: Your identity is derived from your Ed25519 public key. No email, phone number, or personal information is needed.

### Terminal-Native Experience

- **Clean CLI Interface**: Minimal, distraction-free terminal UI built with BubbleTea.
- **No Browser Dependencies**: Pure terminal application that works over SSH.
- **Single Binary**: Easy distribution and deployment—just download and run.
- **Cross-Platform**: Works on Linux, macOS, and Windows.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           DecentChat Architecture                        │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────┐                              ┌─────────────────┐
│    User A       │                              │    User B       │
│   (Terminal)    │                              │   (Terminal)    │
│                 │                              │                 │
│  ┌───────────┐  │                              │  ┌───────────┐  │
│  │  UI Layer │  │                              │  │  UI Layer │  │
│  │(BubbleTea)│  │                              │  │(BubbleTea)│  │
│  └─────┬─────┘  │                              │  └─────┬─────┘  │
│        │        │                              │        │        │
│  ┌─────▼─────┐  │      Encrypted WebSocket     │  ┌─────▼─────┐  │
│  │  Network  │◄─┼──────────────────────────────┼─►│  Network  │  │
│  │  Manager  │  │    (via Cloudflare Tunnel)   │  │  Manager  │  │
│  └─────┬─────┘  │                              │  └─────┬─────┘  │
│        │        │                              │        │        │
│  ┌─────▼─────┐  │      Signaling Server        │  ┌─────▼─────┐  │
│  │ Signaling │  │         (Supabase)           │  │ Signaling │  │
│  │  Client   │◄─┼──────────────────────────────┼─►│  Client   │  │
│  └───────────┘  │  Presence & tunnel URLs only │  └───────────┘  │
└─────────────────┘                              └─────────────────┘
```

### Connection Flow

```
┌──────────┐        ┌──────────────┐        ┌──────────────┐        ┌──────────┐
│  User A  │        │  Cloudflare  │        │   Supabase   │        │  User B  │
│ (caller) │        │    Tunnel    │        │  (signaling) │        │ (callee) │
└────┬─────┘        └──────┬───────┘        └──────┬───────┘        └────┬─────┘
     │                     │                       │                     │
     │ Start local server + cloudflared tunnel                     │
     │────────────────────►│                       │                     │
     │                     │                       │                     │
     │ Publish user_id, public keys        Register presence              │
     │ and tunnel_url ────────────────────────────►│────────────────────►│
     │                     │                       │                     │
     │ /list (GetOnlineUsers)                      │                     │
     │────────────────────────────────────────────►│                     │
     │                     │                       │                     │
     │ /connect B: dial wss://<B's tunnel>/ws with X-Peer-ID header      │
     │────────────────────►│───────────────────────────────────────────►│
     │                     │                       │                     │
     │                     │   Connection held as  │                     │
     │                     │   "pending" until B   │                     │
     │                     │   types /accept       │                     │
     │                     │                       │                     │
     │◄═══════════════════════════════════════════════════════════════►│
     │         Direct encrypted P2P connection (AES-256-GCM)          │
     │◄═══════════════════════════════════════════════════════════════►│
```

> **Note:** Cloudflare *quick* tunnels (`trycloudflare.com`) are ephemeral—each session gets a new random URL, which is published to Supabase on startup and cleared on exit. If your machine goes offline abruptly, the tunnel URL may remain stale in Supabase for a short time until it is overwritten or cleaned up.

---

## Security

### Encryption Layer

All messages are encrypted and signed at the application level, on top of the TLS-secured transport (WSS via Cloudflare):

#### 1. Key Exchange (X25519)

Each user generates an X25519 keypair on first launch. The shared secret is derived statically from your private key and the peer's published public key:

```
User A                              User B
--------                            --------
X25519_Private_A                    X25519_Private_B
X25519_Public_A  ──► Supabase       X25519_Public_B  ──► Supabase

Shared_Secret_A = X25519(X25519_Private_A, X25519_Public_B)
Shared_Secret_B = X25519(X25519_Private_B, X25519_Public_A)
// Shared_Secret_A == Shared_Secret_B
```

#### 2. Message Encryption (AES-256-GCM)

- Each message is encrypted with the shared secret derived above.
- A fresh random nonce is generated for every message.
- The GCM authentication tag protects integrity and authenticity in transit.

#### 3. Message Signing (Ed25519)

- Every message is signed with the sender's Ed25519 private key.
- The recipient verifies the signature against the sender's published identity key **before decrypting is considered valid**—sender authentication is enforced on every message.

### Key Storage

```
~/.decentchat/
├── identity.enc    # AES-256-GCM encrypted identity file (X25519 + Ed25519 keys, trusted_peers)
├── .key            # Local symmetric key used to encrypt identity.enc (0600 permissions)
├── trusted_peers   # TOFU database lives inside identity.enc
└── cloudflared*    # Auto-downloaded cloudflared binary (if not found in PATH)
```

- Keys are generated locally on first launch and never leave the device.
- Identity is backed up simply by copying the `~/.decentchat/` directory to another machine.

### Trust Model (Honest Limitations)

DecentChat uses **self-certifying identities**: your user ID is the first 16 hex characters of the SHA-256 hash of your Ed25519 public key. Message authenticity is verified on every message, so peers cannot forge messages from someone else's identity. However, be aware of the current limitations:

- **Key authenticity depends on the signaling server.** Public keys are fetched from Supabase; a malicious actor who can tamper with your row there could swap keys. DecentChat includes a Trust-on-First-Use (TOFU) database (`trusted_peers`) with key-change detection logic built in, but **it is not yet enforced in the connection flow**—this is planned for an upcoming release. Until then, treat the connection as secure against passive eavesdropping, not against an active signaling-server MITM.
- **No forward secrecy yet.** The session key is derived statically from long-lived X25519 keys, so compromise of the private keys would compromise past sessions. Ephemeral key exchange is on the roadmap.
- **No replay protection yet.** Message timestamps are included but not currently validated.

We document these limitations rather than hide them. Contributions in these areas are very welcome.

---

## Installation

### Prerequisites

Before installing DecentChat, ensure you have the following prerequisites in place:

- **Go 1.21+**: Required for building from source. You can download Go from [golang.org/dl](https://golang.org/dl/)
- **Supabase Account**: Free tier works perfectly for signaling. Sign up at [supabase.com](https://supabase.com)
- **Internet access**: Needed the first time to auto-download `cloudflared` (the binary is cached in `~/.decentchat/`)

> `cloudflared` is auto-downloaded at runtime for Windows (amd64) and Linux (amd64/arm64). On macOS, or for any other platform, install `cloudflared` manually first (e.g. `brew install cloudflared`)—DecentChat will use it from your `PATH`.

### Option 1: Build from Source

```bash
# Clone the repository
git clone https://github.com/HiwarkhedePrasad/DecentChat-Terminal.git
cd DecentChat-Terminal

# Install dependencies
go mod tidy

# Build the application
go build -o decentchat ./cmd

# Run
./decentchat
```

### Option 2: Quick Setup Script

For a streamlined installation experience on Unix-like systems, use the included setup script:

```bash
git clone https://github.com/HiwarkhedePrasad/DecentChat-Terminal.git
cd DecentChat-Terminal
chmod +x setup.sh
./setup.sh
```

The setup script checks for Go, verifies environment variables, downloads dependencies, and builds the application automatically.

### Option 3: Cross-Compile for Different Platforms

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 go build -o decentchat-linux ./cmd

# macOS (amd64 - Intel)
GOOS=darwin GOARCH=amd64 go build -o decentchat-macos ./cmd

# macOS (arm64 - Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o decentchat-macos-arm64 ./cmd

# Windows
GOOS=windows GOARCH=amd64 go build -o decentchat.exe ./cmd
```

### Option 4: Windows PowerShell Build

For Windows users, a PowerShell build script is provided that automatically embeds credentials:

```powershell
.\build.ps1
```

This script loads environment variables from `.env` and builds an executable with embedded Supabase credentials.

---

## Quick Start

### 1. Set Up Supabase

Create a free Supabase project and run the schema from [`supabase/schema.sql`](supabase/schema.sql) in the Supabase SQL Editor. The essential part:

```sql
-- See supabase/schema.sql for the complete schema (RLS policies, cleanup helpers)

CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    public_identity_key TEXT NOT NULL,
    public_enc_key TEXT NOT NULL,
    tunnel_url TEXT DEFAULT '',
    online_status BOOLEAN DEFAULT false,
    last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

**Important:** the schema's RLS policies intentionally allow public read/write so that a Supabase anon key is sufficient for signaling. This is fine for DecentChat's threat model because only *public* keys and tunnel URLs are stored—but it also means anyone can register or overwrite rows on your project. Use your own Supabase project and share it only with the people you chat with.

Optionally, enable the `pg_cron` extension and uncomment the `cron.schedule(...)` line in the schema to automatically mark idle users as offline (rows older than 2 minutes).

### 2. Configure Environment

Create a `.env` file in the project directory with your Supabase credentials:

```env
SUPABASE_URL=https://your-project.supabase.co
SUPABASE_KEY=your-anon-key
```

You can find these values in your Supabase dashboard under **Project Settings → API**. DecentChat reads `SUPABASE_URL` and `SUPABASE_KEY` from environment variables or a `.env` file in the current directory.

### 3. Run DecentChat

```bash
./decentchat
```

On first launch, DecentChat will:
1. Generate your cryptographic keypairs (X25519 + Ed25519)
2. Store the encrypted identity in `~/.decentchat/`
3. Derive your user ID from your public key
4. Start a local WebSocket server and establish a Cloudflare quick tunnel
5. Register with the signaling server, publishing your tunnel URL and public keys

---

## Usage

### CLI Flags

Use these flags before starting the interactive terminal UI:

| Flag | Description | Example |
|------|-------------|---------|
| `-h`, `--help` | Show CLI help and exit | `decentchat --help` |
| `-v`, `--version` | Show app version and exit | `decentchat --version` |

### Commands

DecentChat provides a simple command interface. Commands are typed at the prompt:

| Command | Description | Example |
|---------|-------------|---------|
| `/list` | Show all online users | `/list` |
| `/connect <id>` | Dial a peer's tunnel and send a connection request | `/connect a94f3d21` |
| `/disconnect` | End current connection | `/disconnect` |
| `/accept` | Accept an incoming connection request | `/accept` |
| `/decline` | Decline an incoming connection request | `/decline` |
| `/status` | Show connection status | `/status` |
| `/clear` | Clear chat messages from screen | `/clear` |
| `/help` | Display available commands | `/help` |
| `/exit`, `/quit` | Quit the application | `/exit` |

You can only be connected to one peer at a time (use `/disconnect` first to switch peers). Incoming connections are held as "pending" until you `/accept` or `/decline` them.

### Example Session

```
██████╗ ███████╗███████╗ ██████╗ ██████╗ ██████╗ ██████╗ ███╗   ██╗
██╔══██╗██╔════╝██╔════╝██╔════╝██╔═══██╗██╔══██╗██╔══██╗████╗  ██║
██║  ██║█████╗  ███████╗██║     ██║   ██║██║  ██║██║  ██║██╔██╗ ██║
██║  ██║██╔══╝  ╚════██║██║     ██║   ██║██║  ██║██║  ██║██║╚██╗██║
██████╔╝███████╗███████║╚██████╗╚██████╔╝██████╔╝██████╔╝██║ ╚████║
╚═════╝ ╚══════╝╚══════╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚═╝  ╚═══╝

Version 0.1.0 - Semi-Decentralized Encrypted Terminal Chat
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

User ID: a94f3d21
Initializing...
Starting Cloudflare secure tunnel...
Local tunnel established: wss://example-random-words.trycloudflare.com/ws

[System] Registered with signaling server
[System] Status: Online

> /list
[System] Online users:
  • b7e2c45f
  • d3a9f882
  • e5c1b67a

> /connect b7e2c45f
[System] Connecting to b7e2c45f...
✓ Connected to b7e2c45f - you can now chat securely

[You] Hey! How's it going?
[Peer] Hi! Everything's great. Loving this encrypted chat!
[You] No servers storing our messages. Pretty cool, right?
[Peer] Exactly! True P2P with end-to-end encryption.

> /disconnect
[System] Disconnected from peer

> /exit
[System] Goodbye!
```

---

## Technical Details

### Technology Stack

| Component | Technology | Purpose |
|-----------|------------|---------|
| **Language** | Go 1.21+ | Core application - performance, simplicity, excellent cryptography support |
| **Terminal UI** | BubbleTea | Interactive CLI interface with a reactive, Elm-inspired architecture |
| **Styling** | Lipgloss | Terminal styling and layout |
| **Cryptography** | golang.org/x/crypto + stdlib | X25519 key exchange, Ed25519 signing, AES-256-GCM message encryption |
| **WebSocket (P2P)** | gorilla/websocket | The direct encrypted peer-to-peer connection over the tunnel |
| **Signaling** | Supabase REST API | Presence, public key and tunnel URL publishing (plain HTTP calls) |
| **NAT Traversal** | Cloudflare Quick Tunnel | Ephemeral public endpoint for inbound peer connections |

### Network Communication

DecentChat uses a hybrid approach to network communication:

- **Cloudflare Quick Tunnel**: `cloudflared` is spawned automatically (downloaded to `~/.decentchat/` if not present in your `PATH`) and exposes the local WebSocket server at a random `*.trycloudflare.com` URL. The URL is scraped from `cloudflared` logs and published to Supabase.
- **WebSocket Protocol**: The transport for chat traffic. The caller sends its user ID in an `X-Peer-ID` header during the handshake; the receiver holds the connection pending until the user accepts.
- **HTTP REST API**: Used for all signaling operations with Supabase (user upserts and queries).

### Session Lifecycle

```
┌─────────────────────────────────────────────────────────────────┐
│                     Session Lifecycle                           │
└─────────────────────────────────────────────────────────────────┘

    ┌──────────┐
    │  Startup │
    └────┬─────┘
         │
         ▼
    ┌──────────┐     ┌──────────────┐     ┌───────────────┐
    │ Register │────►│  Set Online  │────►│  Poll Signals │
    │   with   │     │    Status    │     │  (UI presence │
    │Signaling │     └──────────────┘     │    polling)   │
    └──────────┘                          └───────┬───────┘
                                                  │
                                                  ▼
                                           ┌───────────────┐
                                           │    P2P        │
                                           │  Connection   │
                                           │  via Tunnel   │
                                           │  (encrypted)  │
                                           └───────────────┘
                                                  │
    ┌──────────┐                                  │
    │ Shutdown │◄─────────────────────────────────┘
    └────┬─────┘
         │
         ▼
    ┌──────────────┐     ┌──────────────┐
    │  Clear       │────►│    Exit      │
    │  tunnel URL  │     │  (set        │
    │  + go offline│     │   offline)   │
    └──────────────┘     └──────────────┘
```

### Build Options

```bash
# Debug build (with symbols for debugging)
go build -o decentchat ./cmd

# Release build (optimized, stripped binary)
CGO_ENABLED=0 go build -ldflags="-s -w" -o decentchat ./cmd

# With version info embedded
go build -ldflags="-X main.Version=$(git describe --tags)" -o decentchat ./cmd
```

---

## Project Structure

The project follows Go conventions for project layout, with a clear separation of concerns:

```
DecentChat-Terminal/
├── cmd/
│   └── main.go              # Application entry point, banner, CLI flags, shutdown handling
│
├── internal/
│   ├── config/
│   │   └── config.go        # Configuration loading, env variables, .env parsing
│   │
│   ├── crypto/
│   │   └── crypto.go        # X25519, Ed25519, AES-256-GCM operations
│   │
│   ├── identity/
│   │   └── identity.go      # Identity management, encrypted key storage, TOFU helpers
│   │
│   ├── network/
│   │   └── manager.go       # WebSocket server/dialer, cloudflared management,
│   │                        # encryption/signing pipeline, keepalive pings
│   │
│   ├── signaling/
│   │   └── client.go        # Supabase REST API client for signaling
│   │
│   └── ui/
│       └── app.go           # BubbleTea terminal UI, commands, message display
│
├── supabase/
│   └── schema.sql           # Database schema for signaling server (users table, RLS)
│
├── .env                     # Environment configuration (not committed)
├── go.mod                   # Go module definition
├── go.sum                   # Dependency checksums
├── setup.sh                 # Quick setup script for Unix-like systems
├── build.ps1                # Build script for Windows PowerShell
└── README.md                # This file
```

### Module Responsibilities

Each module has a clearly defined responsibility:

| Module | Responsibility |
|--------|----------------|
| `cmd` | Application bootstrap, banner display, CLI flags, graceful shutdown handling |
| `config` | Environment loading, .env file parsing, configuration defaults |
| `crypto` | All cryptographic operations (key generation, encryption, signing, verification) |
| `identity` | User identity management, local encrypted key storage, trusted peers database |
| `network` | WebSocket server/dialer, Cloudflare tunnel management, encrypted connection handling |
| `signaling` | Communication with Supabase for presence and connection establishment |
| `ui` | Terminal user interface, command processing, message display, user interactions |

---

## Roadmap

### Current Version (v0.1.0)

- ✅ End-to-end encrypted P2P messaging (X25519 + AES-256-GCM + Ed25519 signatures)
- ✅ Automatic Cloudflare quick tunnel integration for NAT traversal (with cloudflared auto-download)
- ✅ Terminal UI with BubbleTea
- ✅ Single peer-to-peer connections with accept/decline flow
- ✅ Cross-platform support (Linux, macOS, Windows)
- ✅ Local encrypted identity storage

### Planned Features

| Feature | Status | Description |
|---------|--------|-------------|
| TOFU Enforcement | 🔜 Planned | Wire the existing trusted-peers database into the connection flow, with key-change (possible MITM) alerts |
| Group Chat | 🔜 Planned | Multi-peer mesh network for group conversations |
| File Transfer | 🔜 Planned | Secure file sharing over encrypted P2P connections |
| Message History | 🔜 Planned | Local encrypted message history with export functionality |
| Multi-Connection | 🔜 Planned | Multiple simultaneous peer connections |
| Delivery Receipts | 🔜 Planned | Message delivery and read confirmations |
| Offline Queue | 🔜 Planned | Store messages for offline peers |
| Forward Secrecy | 🔜 Planned | Ephemeral session key exchange (X25519 handshake with one-time keys) |
| Replay Protection | 🔜 Planned | Validate message timestamps / nonces to reject replayed messages |
| Custom TURN Support | 📋 Future | Support for custom TURN servers in restrictive networks |
| Voice Messages | 📋 Future | Audio message recording and playback |
| Plugin System | 📋 Future | Extensible plugin architecture for custom functionality |

---

## Contributing

We welcome contributions from the community! Whether you're fixing bugs, adding features, or improving documentation, your help is appreciated.

### Getting Started

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Run tests (`go test ./...`)
5. Commit your changes (`git commit -m 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

### Code Style

To maintain code quality and consistency, please follow these guidelines:

- Follow standard Go conventions (`gofmt`, `go vet`)
- Add comments for exported functions and types
- Update documentation for API changes
- Add tests for new functionality
- Keep the security-first philosophy in mind for all changes

### Security Issues

If you discover a security vulnerability, please **do not** open a public issue. Instead, contact the maintainer privately via the contact details on [the maintainer's GitHub profile](https://github.com/HiwarkhedePrasad). We take security seriously and will respond promptly to all security reports.

---

## License

This project is licensed under the MIT License:

```
MIT License

Copyright (c) 2024 DecentChat

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Acknowledgments

DecentChat would not be possible without these excellent open-source projects:

- [BubbleTea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework that makes building interactive CLI applications delightful
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Terminal styling library for beautiful, consistent output
- [Supabase](https://supabase.com) - Open source Firebase alternative that powers our signaling infrastructure
- [golang.org/x/crypto](https://golang.org/x/crypto) - Cryptographic primitives that provide the security foundation
- [Gorilla WebSocket](https://github.com/gorilla/websocket) - WebSocket implementation for Go
- [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/) - Enables P2P connectivity behind NATs

---

<p align="center">
  <strong>DecentChat</strong> — Privacy-first terminal messaging.
</p>

<p align="center">
  Made with ❤️ for privacy enthusiasts
</p>
