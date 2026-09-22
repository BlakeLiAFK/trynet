# trynet

**Expose a local port or directory on a public HTTPS URL through Cloudflare's edge — no Cloudflare account, no signup, no inbound firewall rule.**

[简体中文](README.zh-CN.md)

```
$ trynet

   __                        __
  / /________  ______  ___  / /_
 / __/ ___/ / / / __ \/ _ \/ __/
/ /_/ /  / /_/ / / / /  __/ /_
\__/_/   \__, /_/ /_/\___/\__/
        /____/
  mini cloudflare tunnel  v1.1.0

  ✓ tunnel ready  [lax08]
  ┌───────────────────────────────────────────────────────────┐
  │ https://hydraulic-colleagues-mirror-avi.trycloudflare.com │
  └───────────────────────────────────────────────────────────┘
  serving /home/you/project  ·  via direct

  login  admin / 48310726  ·  upload enabled
```

trynet is a single static binary, written in Go with no CGO. It implements the
Cloudflare quick tunnel protocol directly, so it does not shell out to
`cloudflared` and does not require it to be installed.

---

## Quick start

Share the current directory over HTTPS:

```sh
trynet
```

Share a specific directory:

```sh
trynet -dir ~/Downloads
```

Forward a public URL to a local HTTP/WebSocket server:

```sh
trynet -port 3000
```

Each command prints a `https://<random>.trycloudflare.com` URL. That URL is
live until the process exits. For a WebSocket endpoint, use the same host with
`wss://` and the origin's endpoint path; no extra WebSocket flag is needed.

## Install

Download a binary from the [releases page](https://github.com/BlakeLiAFK/trynet/releases/tag/v1.1.0), or build from
source:

```sh
git clone https://github.com/BlakeLiAFK/trynet.git
cd trynet
go run make.go
```

Go 1.26.8 or newer is required (see `go.mod`). `make.go` builds with
`CGO_ENABLED=0`, `-trimpath` and `-ldflags="-s -w"`; no separate `cloudflared`
installation is needed. For manual cross-compilation, also set `CGO_ENABLED=0`.

To build every supported platform at once, into `dist/`:

```sh
go run make.go -all
```

`make.go` optionally uses UPX on Windows and Linux. It checks the packed file
with `upx -t` and falls back to the unpacked binary if packing fails. macOS is
not UPX-packed. Sizes depend on the target and compiler; release measurements
are recorded in `BUILDINFO.json`, rather than promising a fixed 13 MB to 4 MB ratio.

### v1.1.0 downloads and verification

Windows, macOS and Linux each have **amd64 (x86-64)** and **arm64** builds.
Windows packages use `.zip`; macOS/Linux packages use `.tar.gz`. The release
also includes unpacked fallback packages for targets successfully packed with
UPX. `BUILDINFO.json` records each target's packing status and actual sizes.
All packages contain both READMEs, the MIT license and dependency license files.

After extracting the package, run `./trynet -version` (Windows: `trynet.exe -version`).
Verify downloads against `SHA256SUMS.txt`: `sha256sum -c SHA256SUMS.txt` on Linux,
`shasum -a 256 <downloaded-file>` on macOS, or
`Get-FileHash <downloaded-file> -Algorithm SHA256` in PowerShell. Download all
listed files to use the Linux batch-check command, or check only the selected file.

GitHub Actions tests the code, builds all six targets, checks UPX integrity,
and runs the delivered executables on matching native OS/architecture runners
before publishing. To release another version, push a `vMAJOR.MINOR.PATCH` tag
or run the **Release** workflow manually with the tag name. Published tags are
never silently moved or overwritten.

The new CLI started at v1.0.0. **WebSocket support starts with v1.1.0**; the
v1.0.0 binary really rejected upgrades with `501`. Older Wails application
releases such as v1.0.7 belong to the previous project, not this CLI.

## What you get

### Directory sharing (`-dir`, the default)

An embedded file service, served from the binary itself:

- Browse directories, download files
- Drag-and-drop upload, with per-file progress
- List and grid layouts, with server-generated image thumbnails
- Per-file share links: a scoped, expiring token that grants read access to
  one file and nothing else
- Login is required by default, with a randomly generated password
- Works on phones: responsive layout, 44px touch targets, no hover-only controls

### Port forwarding (`-port`)

A reverse proxy to `127.0.0.1:<port>`. Request and response headers and
streaming bodies pass through. **HTTP and WebSocket are supported.** The
local service must provide the WebSocket endpoint; the built-in file server
is not a WebSocket application server.

For an origin listening at `ws://127.0.0.1:3000/ws`:

```sh
trynet -port 3000
# Use wss://<the-printed-host>.trycloudflare.com/ws in your client.
```

Text, binary, fragmented messages, ping/pong and close frames pass through.
Paths, query strings, Host, Origin, Authorization, cookies and subprotocol
negotiation are retained. Origin rejections such as `401`, `403` and `404`
remain rejections; trynet does not bypass application authentication or Origin
checks. File-sharing options `-user` and `-pass` do not secure a forwarded
application: configure authentication on that application itself.

The bridge streams bytes with two pooled 32 KiB copy buffers per active
connection, not a whole-message cache. This is the copy-buffer allocation,
not a promise that total connection memory is 64 KiB; HTTP/2, TLS, sockets
and Go also use memory. No new Go WebSocket dependency is required.

WebSocket extension offers are passed through, but compression depends on
negotiation across the entire path. A client offering `permessage-deflate`
must also work when the edge or origin declines it. Local HTTP/2 integration
tests verify compressed frames and extension headers; the live acceptance
report records whether compression was actually negotiated rather than
assuming it. Do not force compressed frames without successful negotiation.

Use application heartbeats and client reconnection. Re-establishing the tunnel
does not restore an already broken WebSocket session or replay lost messages.
Raw TCP forwarding, UDP and SSH are **not supported**; they are separate
capabilities and must not be conflated with WebSocket support.

### WebSocket verification

The `WebSocket acceptance` workflow runs the HTTP/2 integration tests 20 times
with Go's race detector, reproduces the old implementation's `501`, and tests
a real public `wss://` connection through a temporary Cloudflare quick tunnel.
It covers 1 MiB binary messages, fragmentation, both ping directions, close
codes/reasons, 16 concurrent clients, origin rejection headers, abrupt
 disconnects and resource cleanup. It only exposes an isolated,
token-protected echo fixture, never the repository or a user directory.

To repeat the tests:

```sh
go test -race -count=20 -run '^TestWebSocket' -timeout=5m ./internal/tunnel
python -m pip install websockets==15.0.1
go build -o trynet .
python scripts/websocket_smoke.py --binary ./trynet --output websocket-live.json
```

The Python package is a **test-only** dependency; users of the binary do not
need Python. The public test needs outbound Internet access. Named/custom-domain
provisioning remains unverified as described below; quick-tunnel WSS tests
do not validate account API operations or every third-party application.

---

## How does it work?

A Cloudflare tunnel inverts the usual client/server roles. trynet dials out to
a Cloudflare edge node on port 7844 over TLS, then runs an **HTTP/2 server on
that outbound connection**. The edge sends requests down the connection it
received; trynet answers them from the local origin. Nothing listens on a
public port, so no inbound firewall rule and no port forwarding is needed.

For WebSockets, the public HTTP/1.1 `101` upgrade is represented by an HTTP/2
stream inside the tunnel. trynet performs the local origin upgrade, validates
the handshake, serializes the response headers for the edge and bridges both
frame directions. It does not try to hijack an HTTP/2 response writer.

Registration happens over a Cap'n Proto RPC control stream, the same protocol
`cloudflared` speaks. Credentials for a quick tunnel come from
`https://api.trycloudflare.com/tunnel`, which requires no authentication.

### Proxy handling

Many networks cannot reach the edge directly. trynet probes the available
routes and uses the first one that works, in this order:

1. Direct connection
2. `HTTPS_PROXY` / `ALL_PROXY` / `HTTP_PROXY` (and their lowercase forms)
3. SOCKS5 proxies found in the environment

The working route is remembered and tried first on reconnect. Proxy diagnostics
are printed **only when every route fails** — a working setup stays quiet. When
all routes fail, trynet prints the platform-specific command to retry through a
proxy.

HTTP `CONNECT` proxies frequently cannot reach port 7844. If the edge is
unreachable through one, a SOCKS5 proxy usually works.

### Reconnection

Connections drop. trynet rotates between edge regions with exponential backoff
(3s, doubling, capped at 30s; reset after any successful registration). The
public hostname is preserved across reconnects — a dropped connection does not
change the URL.

### Quick tunnel lifetime

**A quick tunnel hostname stays claimable for roughly 12–16 minutes after the
process exits.** Measured, not documented upstream: the hostname returned HTTP
530 through the 12-minute mark and NXDOMAIN by 16 minutes.

trynet saves quick tunnel credentials to `.trynet.json` in the working
directory (mode 0600) and reuses them on the next start, so restarting within
that window keeps the same URL. It does not hard-code the window: it tries the
saved credentials, and requests a new tunnel only if the edge rejects them.
`-new` forces a fresh tunnel.

Add `.trynet.json` to `.gitignore`.

---

## How is it different from cloudflared?

| | trynet | cloudflared | ngrok | localtunnel |
|---|---|---|---|---|
| Account required | No | No, for quick tunnels | Yes | No |
| Built-in file server | Yes | No | No | No |
| File upload | Yes | No | No | No |
| Auth on the file server | Yes, by default | n/a | n/a | n/a |
| Fixed custom domain | Yes, automated (unverified) | Yes, manual setup | Yes | No |
| Proxy auto-detection | Yes | Environment variables only | Environment variables only | No |
| WebSocket | Yes, since CLI v1.1.0 | Yes | Yes | Yes |

Raw TCP, UDP and SSH forwarding are not implemented in trynet. They are not
included in the WebSocket row; check the other tools' protocol-specific
requirements rather than treating these protocols as one feature.

*Other projects' columns describe their documented behaviour and their free
tiers change over time; check their own documentation before relying on a
cell here.*

trynet is not a replacement for `cloudflared`. `cloudflared` is the supported
production tool, with access policies, load balancing, ingress rules, metrics
and a managed dashboard. trynet implements one narrow path — quick tunnels, plus
optional named tunnels — in as little code as that takes, and adds a file
service on top.

Use `cloudflared` to run a service. Use trynet to hand someone a URL.

---

## Does it need a Cloudflare account?

Not for the default mode. Quick tunnels are anonymous.

An account and an API token are required only for `-domain`, which publishes on
a domain you already own.

---

## File service

### Authentication

Login is on by default. The username is `admin` and the password is 8 random
digits, printed at startup. Digits keep it easy to read aloud and to type on a
phone keypad.

```sh
trynet -dir ~/share -user alice -pass hunter2
```

Sessions are server-side: logging in exchanges credentials for an opaque
32-byte token stored in an `HttpOnly; Secure; SameSite=Strict` cookie, valid
for 12 hours with sliding renewal. HTTP Basic auth is not accepted.

`-public` disables authentication entirely. Anyone with the URL gets in.

### Share links

A share link grants read access to **one file**, for **one hour**, and cannot be
used to write:

```sh
curl -O "https://<host>/data/report.pdf?t=<token>"
```

The token is bound to the path it was issued for. Presenting it for any other
path returns 401. It is valid for `GET` and `HEAD` only, so a share link can
never become an upload endpoint, and it does not renew on use.

Tokens live in memory and are lost when the process exits. Issuing is refused
for directories, for filtered paths, and for anything outside the shared
directory.

### Uploads

Uploads are accepted by default when authentication is on, up to 100 MB per
file. Existing files are never overwritten.

`-read-only` turns uploads off. Under `-public` they are off unless `-upload`
is also given — a publicly writable directory should not be a side effect of a
single flag.

### Content filtering

Paths are filtered on read **and** write, for listings and downloads alike, so
a filtered file is never linked and never served.

Anything beginning with `.` is filtered, plus these names, case-insensitively:

```
.git  .svn  .hg  .ssh  .aws  .gnupg  .kube  .docker
.env  .netrc  .npmrc  .pypirc  .htpasswd  .trynet.json
id_rsa  id_dsa  id_ecdsa  id_ed25519  credentials  secring.gpg
```

And these extensions:

```
.pem  .key  .pfx  .p12  .jks  .keystore  .kdbx  .ppk
```

The list is deliberately short. A long list looks thorough but only blocks what
someone thought of; the real protection is not sharing a directory that holds
secrets. `-bypass` disables filtering entirely and prints a warning.

### Thumbnails

JPEG, PNG and GIF images get a server-generated thumbnail, 160px on the long
edge, box-filtered and re-encoded as JPEG. Source files above 12 MB are
skipped: decoding cost scales with pixel count rather than file size, and this
process may be forwarding tunnel traffic at the same time.

HEIC, WebP and AVIF are not supported — Go's standard library cannot decode
them, and they are not worth a third-party dependency here. Those files fall
back to a file-type icon.

---

## Fixed domain (`-domain`)

> **Unverified.** This mode has unit tests and tests against a fake API server,
> but it has never been run against the real Cloudflare API. Treat it as
> experimental and check what it created in your dashboard.

Publish on a domain you own, instead of a random `trycloudflare.com` hostname:

```sh
export CLOUDFLARE_API_TOKEN=...
trynet -dir ~/share -domain files.example.com
```

trynet creates a named tunnel and a proxied CNAME record for the hostname, or
reuses them if they already exist. `-teardown` deletes both on exit — but only
resources trynet created itself, never ones it found and reused.

The API token needs these permissions:

| Scope | Permission |
|---|---|
| Account | `Cloudflare Tunnel: Edit` |
| Zone | `DNS: Edit` |

`-account-id` is only needed when the token can reach more than one account.

---

## Options

Service options can be set by flag or environment variable. **The flag wins.**
`-version` and `-h` print local information and exit without opening a tunnel.

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `-version` | — | `false` | Print version and commit, then exit |
| `-dir` | `TRYNET_DIR` | `.` | Directory to share |
| `-port` | `TRYNET_PORT` | — | Local port to forward instead of a directory |
| `-user` | `TRYNET_USER` | `admin` | File service username |
| `-pass` | `TRYNET_PASS` | random | File service password |
| `-public` | `TRYNET_PUBLIC` | `false` | Serve with no login at all |
| `-read-only` | `TRYNET_READ_ONLY` | `false` | Refuse uploads |
| `-upload` | `TRYNET_UPLOAD` | `false` | Accept uploads even under `-public` |
| `-bypass` | `TRYNET_BYPASS` | `false` | Disable all content filtering |
| `-new` | `TRYNET_NEW` | `false` | Ignore saved credentials, request a new tunnel |
| `-domain` | `TRYNET_DOMAIN` | — | Fixed domain to publish on |
| `-api-token` | `CLOUDFLARE_API_TOKEN` | — | API token, required with `-domain` |
| `-account-id` | `CLOUDFLARE_ACCOUNT_ID` | — | Account id, only if the token spans several |
| `-teardown` | `TRYNET_TEARDOWN` | `false` | Delete the resources `-domain` created, on exit |

`-dir` and `-port` are mutually exclusive. With neither, trynet shares the
current directory.

---

## FAQ

**Is this safe to expose to the internet?**

The URL is unguessable and the file service requires a login by default, but
this is a tool for temporary sharing, not a hardened public service. Do not
point it at a directory containing credentials, and do not leave it running
unattended.

**Why does my HTTP proxy not work?**

HTTP `CONNECT` proxies often refuse or silently fail to forward port 7844. Some
return `200 Connection established` and then forward nothing. Use SOCKS5
proxy; trynet will find one in the environment and suggest it when every route
fails.

**Can I keep the same URL permanently?**

Not with a quick tunnel. Restarting within about 12–16 minutes reuses the saved
credentials and the same hostname; past that the hostname is released. For a
permanent address, use `-domain` with a domain you own.

**Does the share link work with curl and wget?**

Yes. It is a plain URL with a query parameter and needs no cookie, header or
login. It is read-only and expires after an hour.

**Why is the unpacked executable larger?**

The executable includes the Go runtime, the embedded web UI and its
Cloudflare tunnel dependencies. The builder removes debug information with
`-trimpath -ldflags="-s -w"`, then optionally applies UPX. The exact size and
savings vary by OS, architecture and compiler; see the release's `BUILDINFO.json`.

**Does it support WebSocket, TCP, UDP or SSH?**

**WebSocket: yes, since CLI v1.1.0. Raw TCP/UDP/SSH: no.** Forward the local
HTTP/WebSocket service with `-port` and connect to its path using `wss://` on
the printed hostname. The v1.0.0 README's `501` warning described that older
binary correctly; changing documentation alone would not add support. Upgrade
the executable too. For the test scope and limitations, see WebSocket verification.

---

## Acknowledgements

The tunnel protocol implementation follows
[cloudflare/cloudflared](https://github.com/cloudflare/cloudflared)
(Apache-2.0), which remains the reference for how quick tunnels work.

## License

MIT. See [LICENSE](LICENSE).
