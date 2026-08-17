# gotun — reverse tunnel (thay frpc/frps) — Design

- **Ngày:** 2026-08-17
- **Module:** `github.com/mannk98/gotun`
- **Trạng thái:** design đã duyệt (brainstorming) → chờ writing-plans
- **Mục tiêu người dùng:** thay cặp frpc/frps trong `minitapps/apps/vnccont` bằng 2 binary Go nhỏ tự viết, cấu hình tối giản (giấu được secret), học được security + high-load networking.

---

## 1. Bối cảnh & vấn đề

`vnccont` chạy KDE + TigerVNC → noVNC (nginx `:8080`) + sshd (`:22`), nằm **sau NAT / egress hạn chế** (corporate CA rải khắp). Không có inbound public.

Hiện tại dùng **frpc** quay ra **frps** để expose noVNC + SSH, rồi gọi một **nginxgen API** để server tự sinh vhost nginx (public TLS). Phía server là **3 thứ**: frps + nginx + nginxgen API. Nhược điểm khiến người dùng muốn thay:

- `frpc.toml` + env phơi `serverAddr`, mux, `FRP_SECRET`, `NGINX_API_SECRET` — khó giấu.
- Build frpc cần **CGO cross-compile arm64** (cả một stage nặng trong Dockerfile).
- frp là dependency to, khó kiểm soát bề mặt.

## 2. Mục tiêu / Non-goals

**Mục tiêu (v1):**
- 2 binary Go thuần (không CGO): `proxyc` (client, trong vnccont) + `proxyd` (server, host public).
- Reverse tunnel: client quay RA, giữ **1 kết nối**, server đẩy khách public ngược qua tunnel.
- Expose được **noVNC (HTTP 8080)** và **SSH (22)**.
- Transport mặc định **WSS:443** (xuyên proxy corporate); lõi **transport-agnostic** để cắm raw-TLS/QUIC sau.
- Config client teo còn: `SERVER`, `TOKEN`, danh sách service (env). Không toml phơi secret.
- Bỏ stage build frp + CGO arm64 khỏi Dockerfile.

**Non-goals (hoãn sang v2+):**
- TLS-termination + hostname/SNI routing + ACME autocert ở server (thay nginx+nginxgen).
- HTTP-CONNECT-for-SSH (kiểu mux cũ).
- QUIC/UDP transport.
- Multi-tenant dashboard, quota UI.

## 3. Kiến trúc tổng thể

```
   [vnccont, sau NAT]                          [host public]
   ┌────────────────────┐                    ┌───────────────────────────┐
   │ noVNC :8080 (nginx) │   1 conn WSS:443   │  proxyd                   │
   │ sshd  :22           │◀──────────────────▶│  ├─ tunnel listener (WSS) │
   │                     │  (yamux, giữ mãi)  │  ├─ registry: id→session  │
   │ proxyc ─────────────┼────────────────────┤  └─ public listeners:     │
   │  └ dial local svc   │  nhiều stream      │     :7001→novnc :7002→ssh │
   └────────────────────┘  trong 1 conn       └───────────────────────────┘
          ▲ client quay RA                          ▲ khách public vào
```

- Client mở **đúng 1** kết nối ra ngoài, giữ mãi.
- Mỗi khách public = **1 yamux stream** mở ngược từ server về client.
- `proxyd` = 3 phần: *tunnel listener* (nhận client, auth, dựng yamux) · *registry* (client_id → session + services) · *public relay* (khách vào → mở stream → pipe).
- `proxyc` = 2 phần: *dialer có reconnect/backoff* · *stream handler* (nhận stream → đọc header → dial `127.0.0.1:<svc>` → pipe).

## 4. Giao thức (3 tầng)

### 4.1 Transport — interface (điểm gỡ thế bí)

```go
// Client side
type Dialer interface { Dial(ctx context.Context) (net.Conn, error) }
// Server side
type Listener interface { Accept() (net.Conn, error); Close() error }
```

- Impl v1: `WSSDialer`/`WSSListener` (mặc định) + `TLSDialer`/`TLSListener` (thay thế).
- `coder/websocket` cho `websocket.NetConn(ctx, c, websocket.MessageBinary)` → biến WS thành `net.Conn`, nên tầng trên (yamux) chạy y hệt bất kể TLS trần hay WSS. Đổi transport = đổi 1 dòng config.
- WSS chọn vì xuyên được HTTP forward-proxy (HTTP CONNECT) + TLS-inspection + chỉ cần 443. `proxyc` tôn trọng `HTTPS_PROXY` để đi qua proxy công ty.

### 4.2 Multiplexing — yamux

- Sau khi có `net.Conn`: `yamux.Client(conn)` (proxyc) / `yamux.Server(conn)` (proxyd).
- Server `session.Open()` 1 stream cho mỗi khách public; client `session.Accept()` nhận.
- yamux lo flow-control (WindowUpdate) + keepalive (Ping) + phát hiện đứt.
- Ghi nhận hạn chế: mọi stream trên 1 TCP ⇒ head-of-line blocking ở tầng TCP (chấp nhận được ở quy mô noVNC/SSH; QUIC gỡ điều này ở phase sau).

### 4.3 Control + auth (nằm trên yamux)

**Hello** — bản tin đầu tiên, JSON một dòng, client → server:
```json
{"token":"<secret>","client_id":"<uuid>",
 "services":[{"name":"novnc","local":"127.0.0.1:8080"},
             {"name":"ssh","local":"127.0.0.1:22"}]}
```
Server verify `token`; nếu OK → mở public listener cho từng service (auto-allocate port từ dải cấu hình, hoặc dùng port client xin) → lưu `registry[client_id]` → trả **hello-ack**:
```json
{"ok":true,"endpoints":{"novnc":"<host>:7001","ssh":"<host>:7002"}}
```
`proxyc` log endpoints ra để người dùng biết vào đâu.

**Stream header** — mỗi stream server mở, byte đầu là tên service + `\n` (vd `novnc\n`). `proxyc` đọc header → dial `local` tương ứng → `io.Copy` 2 chiều.

**Auth v1** = pre-shared **token** (env/file, xoay vòng dễ). **mTLS** (client cert do CA của bạn ký) là mode mạnh hơn, optional, thêm sau qua cùng transport interface.

### 4.4 Reconnect / keepalive

- yamux Ping phát hiện conn chết.
- `proxyc` vòng lặp dial lại với **exponential backoff có cap** (hand-rolled, không thêm dep), gửi lại Hello (idempotent).
- Server: khi `client_id` gửi Hello lại (hoặc session cũ đóng) → **đóng listener/registry cũ** của client_id đó trước khi dựng mới (tránh rò cổng).

## 5. Public addressing

- **v1 — port-per-service:** server cấp 1 cổng TCP public cho mỗi service (novnc→`:7001`, ssh→`:7002`, dải cấu hình được). Truy cập: `http://<host>:7001` (noVNC, VNC websocket chạy `ws://`), `ssh -p 7002 user@<host>`. **Không TLS/hostname** ở mặt public — nhỏ nhất, đủ test.
- **v2 — hostname/SNI + TLS (hoãn):** server nghe `:443`, ACME autocert, route noVNC theo Host header, route SSH theo SNI hostname riêng. URL đẹp `https://<uuid>.domain/` + TLS. Thay thế nginx + nginxgen → server còn đúng 1 binary.

## 6. Bảo mật

- **Config client tối giản:** `GOTUN_SERVER=wss://host:443`, `GOTUN_TOKEN=<env/file>`, `GOTUN_SERVICES=novnc=127.0.0.1:8080,ssh=127.0.0.1:22`. Hết. Không còn toml phơi mux/secret — đây là mục tiêu "giấu config".
- **Kênh tunnel luôn mã hoá** (WSS/TLS), kể cả khi mặt public v1 là HTTP trần.
- Server **chỉ expose service client đăng ký**; token verify ở hello; log mọi kết nối; (v2) rate-limit theo token, mTLS.
- Token/secret nạp qua env hoặc file mode 600, không bake vào image.

## 7. Repo layout & thư viện

```
github.com/mannk98/gotun
├── cmd/proxyc/main.go        # client binary (chạy trong vnccont)
├── cmd/proxyd/main.go        # server binary (host public)
├── internal/tunnel/          # transport interface + WSS/TLS impl + yamux setup + pipe util
├── internal/proto/           # codec Hello + stream header
├── internal/relay/           # server: registry + public listener + relay loop
├── docs/superpowers/…        # spec + plan
└── go.mod                    # go 1.26, module github.com/mannk98/gotun
```

**Thư viện:**
- `github.com/hashicorp/yamux` — multiplexer.
- `github.com/coder/websocket` — WSS + `NetConn` adapter.
- `crypto/tls` (stdlib) — transport raw-TLS + lớp TLS của WSS.
- `github.com/google/uuid` — client_id.
- Backoff: hand-rolled (vài dòng), không thêm dep.
- **Không** phụ thuộc golibs — giữ gotun độc lập, nhẹ, dễ mở source riêng. Log dùng `log/slog` (stdlib).

## 8. Tích hợp vnccont

**Dockerfile:** thay stage `build-frp` (clone fatedier/frp, CGO cross arm64) bằng:
```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.26.2-bookworm AS build-gotun
ARG TARGETARCH
COPY company_ca.crt /usr/local/share/ca-certificates/company_ca.crt
RUN apt update && apt install -y --no-install-recommends ca-certificates git \
    && update-ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/mannk98/gotun.git /build/gotun && cd /build/gotun \
    && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o proxyc ./cmd/proxyc
```
`COPY --from=build-gotun /build/gotun/proxyc /usr/bin/proxyc`. **Bỏ được** toolchain `gcc-aarch64-linux-gnu` (gotun pure-Go, cross-compile chỉ cần GOARCH).

**`services.d/proxy/run.sh`:** teo mạnh — bỏ gen `frpc.toml`, bỏ vòng retry port, **bỏ luôn khối gọi nginxgen API** (v1 không có server nginx). Còn lại: validate env → `exec proxyc` với `GOTUN_SERVER`/`GOTUN_TOKEN`/`GOTUN_SERVICES`.

**Server:** `proxyd` deploy riêng trên host public (systemd/container), đọc `GOTUN_TOKEN` + dải port + (v2) cert.

## 9. Chiến lược test

- **Unit:** codec Hello/stream-header; transport qua `net.Pipe`; relay pipe loopback (echo service); token auth (đúng/sai/thiếu).
- **Integration:** dựng `proxyd` + `proxyc` in-process qua TLS localhost, đăng ký 1 echo-service, nối qua public listener, assert round-trip; test **reconnect** (kill session → client nối lại → vẫn phục vụ); test **multi-stream** (nhiều khách đồng thời trên 1 conn).
- **e2e (thủ công/CI sau):** docker — vnccont (proxyc) + proxyd trên host, mở noVNC thật + `ssh` thật.
- Implement theo TDD qua `go-tdd-sprint`.

## 10. Phasing

**v1 (bản này):** transport WSS + raw-TLS (interface) · yamux · Hello/token · port-per-service · reconnect · tích hợp vnccont · test unit+integration. Mặt public HTTP trần.

**Hoãn:** ACME/TLS-termination + hostname/SNI routing (thay nginx+nginxgen) · HTTP-CONNECT-for-SSH · QUIC transport · mTLS · rate-limit/quota · multi-tenant.

## 11. Open questions / tương lai

- Port allocation v1: server auto-allocate từ dải, hay client xin port cố định (ổn định URL khi restart)? → mặc định auto-allocate + trả về ack; cho phép client "xin" port ưu tiên.
- v2 routing: dùng SNI proxy tự viết trong proxyd hay đặt một reverse-proxy nhẹ phía trước? → quyết ở spec v2.
- Có gộp `proxyc`+`proxyd` thành 1 binary theo mode (như chisel) không? → v1 tách 2 cho rõ ràng; cân nhắc gộp sau.
