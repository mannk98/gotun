# gotun

A tiny **reverse tunnel** in Go: expose TCP services running on a host behind
NAT / a restrictive firewall to a public server, over a **single outbound
connection**. Two static binaries, no CGO.

- **`proxyc`** (client) runs next to your services (behind NAT). It dials **out**
  to the server over WebSocket-over-TLS and keeps one connection open.
- **`proxyd`** (server) runs on a public host. For each service the client
  registers, it opens a public TCP port; every visitor to that port is spliced
  back to the client's local service over the tunnel.

Each visitor becomes an independent [yamux](https://github.com/hashicorp/yamux)
stream multiplexed over the one client→server connection — so a single outbound
port (443) carries any number of concurrent connections, and it traverses HTTP
`CONNECT` proxies (honours `HTTPS_PROXY`).

```
   [host behind NAT]                         [public host]
   ┌──────────────────┐    1 conn WSS:443    ┌──────────────────────────┐
   │ your svc :8080    │◀────────────────────▶│ proxyd                   │
   │ your svc :22      │   (yamux streams)    │  :7001 → svc:8080        │
   │ proxyc ───────────┼──────────────────────┤  :7002 → svc:22          │
   └──────────────────┘                       └──────────────────────────┘
        client dials OUT                            visitors connect IN
```

## Install / build

Pure Go, no CGO — cross-compiles anywhere:

```bash
go build -o proxyd ./cmd/proxyd
go build -o proxyc ./cmd/proxyc
# or for a Linux target from any host:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o proxyc ./cmd/proxyc
```

## Run

### Server (`proxyd`, public host)

Needs a TLS certificate (the tunnel channel is always encrypted).

| Env | Default | Meaning |
|-----|---------|---------|
| `GOTUN_TOKEN` | — (required) | Shared secret clients must present. |
| `GOTUN_TLS_CERT` / `GOTUN_TLS_KEY` | — (required) | PEM cert + key for the WSS listener. |
| `GOTUN_LISTEN` | `:443` | Address the tunnel listener binds. |
| `GOTUN_PATH` | `/tunnel` | WebSocket upgrade path. |
| `GOTUN_PUBLIC_HOST` | `localhost` | Host advertised back to clients in endpoints. |
| `GOTUN_PORT_MIN` / `GOTUN_PORT_MAX` | `20000` / `20999` | Public port range for registered services. |

```bash
GOTUN_TOKEN=$(openssl rand -hex 16) \
GOTUN_TLS_CERT=/etc/gotun/fullchain.pem GOTUN_TLS_KEY=/etc/gotun/key.pem \
GOTUN_PUBLIC_HOST=tunnel.example.net \
./proxyd
```

### Client (`proxyc`, behind NAT)

| Env | Default | Meaning |
|-----|---------|---------|
| `GOTUN_SERVER` | — (required) | `wss://host:443/tunnel`. |
| `GOTUN_TOKEN` | — (required) | Must match the server. |
| `GOTUN_SERVICES` | — (required) | Comma list `name=host:port` of local services to expose. |
| `GOTUN_CLIENT_ID` | random UUID | Stable id; reconnects replace the old registration. |
| `GOTUN_INSECURE` | — | `1` to skip TLS verification (**testing only**). |

```bash
GOTUN_SERVER=wss://tunnel.example.net:443/tunnel \
GOTUN_TOKEN=<same-token> \
GOTUN_SERVICES="web=127.0.0.1:8080,ssh=127.0.0.1:22" \
./proxyc
# proxyc logs the public endpoints, e.g. web -> tunnel.example.net:20000
```

`HTTPS_PROXY` is honoured — the client tunnels through an HTTP `CONNECT` proxy
when one is set, so it works from restricted-egress networks.

## Security

- The client→server channel is always TLS (WSS). Auth is a shared token,
  compared in constant time, before any port is allocated.
- The client controls exactly which local services are exposed; the server only
  opens ports for what a valid token registered.
- Keep `GOTUN_INSECURE` off outside local testing.

## Status & scope

**v1** — WSS transport (raw-TLS also available internally behind the transport
interface), token auth, port-per-service addressing, automatic reconnect with
backoff. The public side of a service port is plain TCP; put your own TLS in
front if a service needs public HTTPS.

Deliberately **out of scope for v1** (see `docs/superpowers/specs/`): hostname /
SNI routing + ACME (per-service public TLS), QUIC transport, mTLS, graceful
server shutdown / port reclamation on signal.

## Layout

```
cmd/proxyd  cmd/proxyc     the two binaries
internal/proto             wire codec (Hello / Ack / stream header)
internal/tunnel            transport (WSS default + raw-TLS) + yamux + pipe
internal/relay             proxyd: accept, auth, per-service listeners, relay
internal/client            proxyc: dial, register, splice, reconnect
```

Design + implementation notes live in `docs/superpowers/`.
