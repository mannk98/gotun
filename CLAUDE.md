# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

## What this is

`gotun` is a minimal **reverse tunnel**: a client (`proxyc`) behind NAT holds one
outbound WebSocket-over-TLS connection to a public server (`proxyd`); each public
visitor is multiplexed back to the client as a yamux stream and spliced to a
local service. It replaces frp for the simple "expose a NATed host's ports"
case. Pure Go, **no CGO** — both binaries cross-compile for linux/amd64+arm64.

## Build / test (everything runs on the host — pure Go, no container)

```bash
go build ./...
go test ./...                 # unit + the root e2e_test.go
go test ./... -race           # concurrency-sensitive (relay/client/tunnel)
go vet ./... && gofmt -l .     # must be clean
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/...   # cross-build check
```

There is no Makefile and nothing to mock — tests use `net.Pipe`, real localhost
TLS, and an in-process echo service. `e2e_test.go` (package `gotun_test`) stands
up a real `proxyd` + `proxyc` over WSS and asserts a round-trip across two
concurrent streams.

## Layout & responsibilities

```
internal/proto     Wire codec: line-framed JSON Hello/HelloAck + a one-line
                   stream header naming the target service. readLine reads one
                   byte at a time (never over-reads the payload) and caps a line
                   at 64 KiB (maxLineLen) — a guard on the unauthenticated path.
internal/tunnel    Transport-agnostic core. Dialer/Listener interfaces yield a
                   net.Conn; WSS (default, coder/websocket + websocket.NetConn)
                   and raw-TLS both implement them. ClientSession/ServerSession
                   wrap yamux; Pipe splices two conns and closes both.
internal/relay     proxyd. Accept tunnel conn → ServerSession → the client's
                   control stream → verify token → one public TCP listener per
                   service → each visitor gets a fresh yamux stream tagged with
                   the service name → Pipe. Registry maps client_id → session +
                   listeners, with identity-aware replace-on-rehello.
internal/client    proxyc. Dial (with capped-backoff reconnect that resets after
                   a live session) → open control stream → Hello → accept
                   server-opened streams → read header → dial the named local
                   service → Pipe.
cmd/proxyd         Server main: env → relay.Config + WSS listener → Serve.
cmd/proxyc         Client main: env → parse services → client.Config → Run.
```

## Protocol (how the two ends talk)

1. Client dials the transport (WSS by default), wraps it in a **yamux client**
   session, and opens ONE **control stream**. Server accepts that stream FIRST.
2. Client sends `Hello{token, client_id, services[]}` on the control stream;
   server verifies the token (`crypto/subtle` constant-time), allocates a public
   port per service, replies `HelloAck{ok, endpoints}`.
3. Per visitor: **server** opens a yamux stream, writes `"<service>\n"`, the
   client reads it, dials the local addr, and both directions are `Pipe`d.
4. Direction is load-bearing: the **client** only ever opens the control stream;
   the **server** opens all data streams. That's how each side knows which
   `AcceptStream()` it's getting.

## Conventions

- **Deps are deliberate and few:** `hashicorp/yamux`, `coder/websocket`,
  `google/uuid`, otherwise stdlib. **No golibs, no testify.** Logging is
  `log/slog` only. Don't add a dependency for something a few lines cover.
- **Tests:** stdlib `testing`, table-driven, assert real behaviour (round-trips
  over real transports), not mocks. Keep output pristine.
- **Errors:** return + wrap; the relay/handshake paths default to the safe branch
  (close the session, don't serve) on any error.
- **Commits:** Conventional Commits. This is a personal repo — the git identity
  (`mannk <khacman98@gmail.com>`) comes from an `includeIf` on `~/git/gotun/` and
  a personal-key SSH alias; **push over `git@mannk98:` (not `git@github.com:`)**.
- **Gotcha:** a global gitignore excludes `docs/superpowers/` — use `git add -f`
  to commit the spec/plan there.

## v1 scope (don't "fix" these as bugs — they're documented deferrals)

Wiring `ctx`/a `Server.Close()` for graceful shutdown (ports free only at process
exit), hostname/SNI routing + ACME (per-service public TLS), QUIC transport,
mTLS. The transport interface exists precisely so raw-TLS/QUIC can slot in later
without touching relay/client. Full rationale in `docs/superpowers/specs/` and
the task plan in `docs/superpowers/plans/`.

## Consumed by

`gotun` is cloned + built by the `vnccont` image (in the `minitapps` repo) as the
proxy that exposes its in-container noVNC + SSH — the reason the client is
WSS/`HTTPS_PROXY`-aware (it runs behind a restrictive corporate egress).
