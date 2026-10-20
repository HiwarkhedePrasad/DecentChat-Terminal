# DecentChat Roadmap

This document tracks what DecentChat has accomplished and what remains to be done. It is organized into completed work, current known limitations (security debt), short-term priorities, and long-term vision.

---

## ✅ Accomplished

### Core Foundation (v0.1.0)

- [x] **End-to-end encrypted P2P messaging** — every message is encrypted with AES-256-GCM using an X25519-derived shared secret, and signed with Ed25519; recipients verify the signature against the sender's published identity key before displaying a message.
- [x] **X25519 + Ed25519 + AES-256-GCM cryptographic stack** — key generation, exchange, encryption/decryption, signing and verification implemented in `internal/crypto`.
- [x] **Self-certifying user identity** — user ID is derived from the SHA-256 hash of the Ed25519 public key, so no account or personal info is required.
- [x] **Local encrypted identity storage** — keys persisted to `~/.decentchat/identity.enc` (AES-256-GCM encrypted with a local `.key` file, `0600` permissions), rebuilt automatically on subsequent launches (`internal/identity`).
- [x] **TOFU data layer** — trusted-peers database (peer ID → public key) with key-match/key-change helpers implemented inside the identity store.

### Networking & NAT Traversal

- [x] **Local WebSocket server** — random-port listener on `127.0.0.1` using gorilla/websocket, with proper handshake (`X-Peer-ID` header) (`internal/network`).
- [x] **P2P caller/dialer** — direct encrypted WebSocket connections over the tunnel; single active connection with keepalive pings (45s) and write-lock serialization.
- [x] **Automatic Cloudflare quick tunnel** — spawns `cloudflared`, scrapes the ephemeral `*.trycloudflare.com` URL from logs, converts it to a WSS endpoint.
- [x] **cloudflared auto-download** — downloads the correct binary for Windows (amd64) and Linux (amd64/arm64) on first run, cached in `~/.decentchat/`.
- [x] **Incoming-connection flow** — connections upgrade but are held as "pending" until the receiver explicitly `/accept`s or `/decline`s the request.
- [x] **Graceful shutdown** — clears tunnel URL from signaling server, sets the user offline, and terminates the tunnel process on exit (signal handlers + deferred cleanup).

### Signaling

- [x] **Supabase REST signaling client** — presence upserts, tunnel URL publish/clear, online-user listing, individual user lookup (`internal/signaling`).
- [x] **Signaling schema with RLS** — `users` table, indexes, RLS policies, and stale-session cleanup helpers (`supabase/schema.sql`).
- [x] **Stale session design** — last_seen timestamps with a pg_cron-based cleanup recipe included in the schema.

### User Interface

- [x] **BubbleTea terminal UI** — alt-screen, Elm-architecture message model with styled output (Lipgloss) (`internal/ui`).
- [x] **Command set** — `/list`, `/connect <id>`, `/accept`, `/decline`, `/disconnect`, `/status`, `/clear`, `/help`, `/exit` / `/quit`.
- [x] **CLI flags** — `-h/--help` and `-v/--version` with a clean usage screen.
- [x] **Presence polling** — UI polls the signaling server and network callbacks deliver incoming messages, connection, disconnection, and incoming-request events to the TUI.
- [x] **Accept/decline UX** — pending prompt, accept/decline hint messages, connection-state-aware command hints.

### Build & Distribution

- [x] **Cross-platform build paths** — Linux/macOS/Windows cross-compilation commands; `setup.sh` (Unix) and `build.ps1` (Windows with embedded credentials from `.env`).
- [x] **Configuration system** — env vars + `.env` file parsing via `internal/config`; data directory created with `0700` permissions.
- [x] **Documentation** — README with accurate architecture, security model (including honest limitations), quick start, and usage; MIT `LICENSE`.

## ⚠️ Known Limitations (Security Debt)

These are current gaps in the security model that should be prioritized before promoting the use of this software past an "experimental" stage:

- [ ] **TOFU not enforced** — the trusted-peers database and key-change helpers exist in `internal/identity`, but the connection flow (`internal/network`) does not consult them; a key change is not currently surfaced as a warning to the user.
- [ ] **No forward secrecy** — session keys are derived statically from long-lived X25519 keys; compromising private keys would expose past sessions.
- [ ] **No replay protection** — message timestamps are transmitted but never validated; there is no nonce/message-ID deduplication.
- [ ] **Identity file encryption is superficial** — `identity.enc` is encrypted with a key stored right next to it (`~/.decentchat/.key`), so at-rest protection is mainly obfuscation; OS keychains or a passphrase would be more meaningful.
  (Also: `DeriveKeyFromPassword` exists but is unused.)
- [ ] **macOS cloudflared auto-download is a placeholder** — the tgz download path is not extracted; macOS users must install `cloudflared` manually.
- [ ] **Signaling server trusts all writes** — RLS is deliberately open (anon-key friendly), so anyone can register/overwrite rows on a signaling project; suitability depends on deploying your own Supabase project for a trusted circle.
- [ ] **No tests** — the repository contains no test suite despite growing crypto/network logic.

## 🔜 Short-Term Priorities (Next Release)

1. **Enforce TOFU in the connection flow** ⚠️ *security*
   - On first connection: fingerprint the peer's Ed25519 public key, store it, and show the fingerprint to the user for out-of-band comparison.
   - On subsequent connections: reject/warn when the published key does not match the stored one (possible MITM), with an explicit override option.
2. **Replay protection** ⚠️ *security*
   - Validate message timestamps with a sane drift window; track previous message IDs/nonces per session to drop duplicates.
3. **Forward secrecy** ⚠️ *security*
   - Ephemeral X25519 key exchange at connection time (signed with Ed25519 identity keys to bind the handshake), so past messages stay safe if long-term keys leak later.
4. **Test suite**
   - Unit tests for `crypto`, `identity`, `config`; integration test for the message encrypt/sign → verify/decrypt round-trip; CI (GitHub Actions) running `go test ./...`, `gofmt`, and `go vet` on push.
5. **macOS cloudflared support**
   - Extract the `.tgz` during auto-download, or auto-install the signed binary.
6. **Better identity onboarding**
   - Show a QR-code–able or copyable public key fingerprint at startup so peers can verify identities out-of-band.
7. **Robust tunnel lifecycle**
   - Detect cloudflared process death mid-session, reconnect with a fresh tunnel URL, and refresh it in the signaling server automatically.

## 📋 Medium-Term (v0.2 – v0.3)

- [ ] **Message history** — local encrypted chat log with export functionality (opt-in).
- [ ] **Multi-connection** — support multiple simultaneous peer connections with a contact tab/list in the TUI.
- [ ] **Group chat** — multi-peer mesh where pairwise encrypted tunnels are bridged with per-group symmetric keys distributed to authenticated members.
- [ ] **File transfer** — chunked, streamed file sharing over the encrypted channel with progress display.
- [ ] **Delivery receipts** — delivery ack + read receipts as separate signed message types.
- [ ] **Offline message queue** — store-and-forward of messages for peers currently offline (local queue only; optionally encrypted relay rows in Supabase as opt-in).
- [ ] **UI polish** — message timestamps/colors, per-peer nickname aliases, scrolling history beyond screen buffer, /fingerprint, /trust and /untrust commands tying into TOFU.
- [ ] **Faster / more resilient signaling** — selective presence sync (e.g., pub/sub or realtime channel) instead of plain HTTP polling, plus configurable sync interval.

## 🔮 Long-Term / Future

- [ ] **Decentralized signaling options** — DHT-based or libp2p-based peer discovery so the project can run without any Supabase dependency; Supabase becomes merely one optional signal backend.
- [ ] **Custom TURN / relay support** — configurable relay for restrictive networks where Cloudflare quick tunnels are blocked.
- [ ] **Voice messages & calls** — audio capture/playback and (later) real-time voice over encrypted WebRTC or tunnel-based channels.
- [ ] **NAT hole-punching (ICE/STUN)** — reduce reliance on public tunnels for direct connectivity.
- [ ] **Plugin system** — extension architecture for custom message types, bots, and command packs.
- [ ] **Internationalization** — localized UI strings.
- [ ] **Packaging & releases** — signed binaries and GitHub Releases artifacts for macos/linux-windows (amd64/arm64), Homebrew formula, scoop package, and `go install` support.
- [ ] **Security audit** — third-party review of the crypto and connection flows before any 1.0 announcement.

## Milestones Overview

| Milestone | Theme | Definition of Done |
|-----------|-------|--------------------|
| **v0.1.0** (current) | Foundation — E2E chat over tunnels with Supabase signaling | ✅ Shipped |
| **v0.2.0** | Security hardening — TOFU enforcement, replay protection, forward secrecy, tests/CI | All short-term security items merged |
| **v0.3.0** | Richness — history, multi-connection, receipts, offline queue | All medium-term items merged |
| **v1.0.0** | Trust & autonomy — decentralized signaling option, security audit, signed releases | Audit complete, decentralized backend shipped |
