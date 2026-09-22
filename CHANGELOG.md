# Changelog

## CLI v1.1.0 — 2026-09-22

### Added

- Transparent WebSocket forwarding for `-port`, including public `wss://` via Cloudflare's HTTP/2 tunnel stream and an HTTP/1.1 upgrade to the loopback origin.
- Handshake validation, serialized edge response headers, subprotocol/cookie/auth/Origin preservation, text/binary/fragment/control-frame forwarding, bounded pooled copy buffers, cancellation and origin-socket cleanup.
- Real TLS/HTTP2 integration tests, compressed frame passthrough tests, 20 race-detector repetitions, and an automated baseline proving the v1.0.0 implementation returned 501.
- Public WSS acceptance fixture covering 21 check groups, 1 MiB binary messages and 16 concurrent clients. Test credentials and processes are temporary; only a token-guarded echo server is exposed.
- Release gate testing actual Linux amd64 packed and unpacked archives over public WSS before publishing any platform.

### Documentation

- Updated English and Chinese port-forwarding sections, capability table and FAQ; WebSocket is separate from raw TCP, UDP and SSH.
- Added client usage, application-authentication responsibilities, heartbeat/reconnection and extension-negotiation limitations.
- Corrected version metadata for local builds; `make.go` reads its default from embedded `VERSION`.

### Limitations

- CLI v1.0.0 really lacked WebSocket support. Upgrade the executable, not just the README.
- Raw TCP/UDP/SSH forwarding is still unsupported.
- Compression is negotiated end to end. The verified public quick-tunnel run declined `permessage-deflate` and correctly used uncompressed fallback; local HTTP/2 tests separately verified negotiated compressed frames.
- Existing WebSocket sessions cannot survive an edge/tunnel disconnect without application reconnection.
- Custom-domain provisioning, signing and notarization limitations remain unchanged.

## CLI v1.0.0

Initial standalone Go CLI release, replacing the previous Wails project. Existing legacy Wails v1.0.x releases are not versions of this CLI.
