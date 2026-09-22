#!/usr/bin/env python3
"""Live WSS acceptance test; exposes only an isolated token-guarded echo server.
Requires Python 3.11+ and websockets==15.0.1. No Cloudflare account required.
Never exposes repository files. Child process and temporary credentials are
cleaned up on both success and failure.
"""
import argparse
import asyncio
import hashlib
from http import HTTPStatus
import json
import os
from pathlib import Path
import re
import secrets
import sys
import tempfile
import time
import traceback
from urllib.request import urlopen
import websockets
from websockets.asyncio.client import connect
from websockets.asyncio.server import serve
from websockets.exceptions import ConnectionClosed, InvalidStatus

async def run_suite(args, report):
    token = secrets.token_hex(24)
    origin = "https://trynet-acceptance.example"
    active, failures = set(), []

    def passed(name):
        report["checks"].append({"name": name, "status": "passed"})
        print("PASS", name, flush=True)

    def reject(connection, status, text):
        response = connection.respond(HTTPStatus(status), text + "\n")
        response.headers["X-Test-Rejection"] = str(status)
        if status == 401:
            response.headers["WWW-Authenticate"] = 'Bearer realm="trynet-test"'
        return response

    def process_request(connection, request):
        if request.path == "/health":
            return connection.respond(HTTPStatus.OK, "healthy\n")
        if request.headers.get("Authorization") != "Bearer " + token:
            return reject(connection, 401, "authentication required")
        if request.headers.get("Origin") != origin:
            return reject(connection, 403, "origin rejected")
        if not request.path.startswith("/ws"):
            return reject(connection, 404, "no such endpoint")
        if request.headers.get("Cookie") != "test-session=" + token:
            return reject(connection, 403, "cookie missing")
        return None

    async def echo(ws):
        active.add(ws)
        try:
            await ws.send(json.dumps({"path": ws.request.path, "host": ws.request.headers["Host"],
                "origin": ws.request.headers.get("Origin"), "authenticated": True,
                "extension_offer_at_origin": ws.request.headers.get("Sec-WebSocket-Extensions", "")}))
            async for message in ws:
                if message == "__server_ping__":
                    await asyncio.wait_for(await ws.ping(b"server-ping"), 10)
                    await ws.send("server-ping-ok")
                elif message == "__server_close__":
                    await ws.close(1001, "origin closing")
                    return
                elif message == "__abort__":
                    ws.transport.abort()
                    return
                else:
                    await ws.send(message)
        except ConnectionClosed:
            pass
        except Exception as exc:
            failures.append(type(exc).__name__ + ": " + str(exc))
        finally:
            active.discard(ws)

    process, reader_task = None, None
    output = []
    with tempfile.TemporaryDirectory(prefix="trynet-ws-test-") as directory:
        async with serve(echo, "127.0.0.1", 0, process_request=process_request, subprotocols=["chat.v1"],
                         max_size=8*1024*1024, ping_interval=None, close_timeout=3) as server:
            port = server.sockets[0].getsockname()[1]
            if args.local_only:
                host, scheme = f"127.0.0.1:{port}", "ws"
                report["transport"] = "fixture self-test only; no TryNet or Cloudflare"
            else:
                binary = Path(args.binary).resolve()
                if not binary.is_file():
                    raise FileNotFoundError(binary)
                report["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
                env = {k: v for k, v in os.environ.items() if not k.startswith("TRYNET_")}
                process = await asyncio.create_subprocess_exec(str(binary), "-port", str(port), "-new", cwd=directory,
                    env=env, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT)
                ready = asyncio.get_running_loop().create_future()
                async def read_output():
                    async for line in process.stdout:
                        text = re.sub(r"\x1b\[[0-9;]*m", "", line.decode(errors="replace"))
                        output.append(text)
                        match = re.search(r"https://([a-z0-9-]+\.trycloudflare\.com)", text)
                        if match and not ready.done():
                            ready.set_result(match.group(1))
                    if not ready.done():
                        ready.set_exception(RuntimeError("tunnel exited before ready: " + "".join(output[-12:])))
                reader_task = asyncio.create_task(read_output())
                host, scheme = None, "wss"
            try:
                if process is not None:
                    host = await asyncio.wait_for(ready, 120)
                    report["transport"] = "public WSS -> Cloudflare -> TryNet HTTP/2 stream -> loopback WS"
                    # Retry readiness only, never failed protocol assertions.
                    last = None
                    for attempt in range(30):
                        try:
                            def health():
                                with urlopen(f"https://{host}/health", timeout=5) as response:
                                    return response.read()
                            if await asyncio.to_thread(health) == b"healthy\n":
                                break
                        except Exception as exc:
                            last = type(exc).__name__ + ": " + str(exc)
                        await asyncio.sleep(2)
                    else:
                        raise RuntimeError("public HTTP readiness failed: " + str(last))
                    passed("public HTTPS baseline")

                def dial(path="/ws", *, compression=None, headers=None, request_origin=origin):
                    return connect(f"{scheme}://{host}{path}", origin=request_origin, subprotocols=["chat.v1"],
                        compression=compression, additional_headers=headers if headers is not None else
                        {"Authorization": "Bearer " + token, "Cookie": "test-session=" + token},
                        proxy=None, open_timeout=15, close_timeout=5, ping_interval=None, max_size=8*1024*1024)

                async with dial("/ws/a%2Fb?query=hello%20world") as ws:
                    meta = json.loads(await ws.recv())
                    assert meta["path"] == "/ws/a%2Fb?query=hello%20world" and meta["host"] == host, "path/host"
                    assert meta["origin"] == origin and meta["authenticated"] and ws.subprotocol == "chat.v1", "origin/auth/protocol"
                    passed("101 handshake, path/query, Host, Origin, Authorization, Cookie and subprotocol")
                    passed("server-initiated message before client data")
                    for label, message in [("empty text", ""), ("UTF-8 text", "你好，WebSocket 👋"),
                                           ("binary bytes", bytes(range(256))), ("1 MiB binary", bytes(range(256))*4096)]:
                        await ws.send(message)
                        assert await ws.recv() == message, label
                        passed(label)
                    await ws.send(["fragment-1", "分片二", "-3"])
                    assert await ws.recv() == "fragment-1分片二-3", "text fragmentation"
                    passed("fragmented text message")
                    await ws.send([b"\x00\xff", b"\xfe\x01", b"end"])
                    assert await ws.recv() == b"\x00\xff\xfe\x01end", "binary fragmentation"
                    passed("fragmented binary message")
                    await asyncio.wait_for(await ws.ping(b"client-ping"), 10)
                    passed("client ping / origin pong")
                    await ws.send("__server_ping__")
                    assert await ws.recv() == "server-ping-ok", "server ping"
                    passed("origin ping / client pong")
                    for i in range(100):
                        message = f"ordered-message-{i}"
                        await ws.send(message)
                        assert await ws.recv() == message, "message order"
                    passed("100 ordered round trips")
                    await asyncio.sleep(3)
                    await ws.send("after-idle")
                    assert await ws.recv() == "after-idle", "idle resume"
                    passed("idle then resume")
                    await ws.close(1000, "client finished")
                    assert ws.close_code == 1000 and ws.close_reason == "client finished", "client close"
                    passed("normal close code and reason")
                async with dial(compression="deflate") as ws:
                    meta = json.loads(await ws.recv())
                    extension = ws.response.headers.get("Sec-WebSocket-Extensions", "")
                    negotiated = "permessage-deflate" in extension
                    report["compression"] = {
                        "offered_by_client": True, "offered_to_origin": meta["extension_offer_at_origin"],
                        "response_extensions": extension, "negotiated": negotiated,
                    }
                    # RFC6455/7692 allow extension offers to be declined. An edge
                    # may strip the offer; never claim compression was negotiated
                    # just because uncompressed data passed. Local Go integration
                    # separately tests negotiated deflate frames byte-for-byte.
                    if args.local_only:
                        assert negotiated, "fixture should negotiate compression directly"
                    message = "compressible-data-" * 65536
                    await ws.send(message)
                    assert await ws.recv() == message, "compression/fallback data corrupted"
                    passed("compression offer and >1 MiB text: " + ("negotiated deflate" if negotiated else "uncompressed fallback"))
                async with dial() as ws:
                    await ws.recv()
                    await ws.send("__server_close__")
                    await ws.wait_closed()
                    assert ws.close_code == 1001 and ws.close_reason == "origin closing", "origin close"
                    passed("origin-initiated close")
                async def concurrent(index):
                    async with dial(f"/ws/concurrent/{index}") as ws:
                        await ws.recv()
                        for i in range(10):
                            message = f"{index}:{i}"
                            await ws.send(message)
                            assert await ws.recv() == message, "concurrent stream mixup"
                await asyncio.gather(*(concurrent(i) for i in range(16)))
                passed("16 concurrent connections, 160 isolated echoes")
                for status, path, headers, request_origin in [
                    (401, "/ws", {}, origin), (403, "/ws", None, "https://untrusted.example"), (404, "/missing", None, origin),
                ]:
                    try:
                        async with dial(path, headers=headers, request_origin=request_origin):
                            raise AssertionError(f"expected origin {status}")
                    except InvalidStatus as exc:
                        assert exc.response.status_code == status, f"origin {status} became {exc.response.status_code}"
                        assert exc.response.headers["X-Test-Rejection"] == str(status), "rejection headers"
                        if status == 401:
                            assert exc.response.headers["WWW-Authenticate"] == 'Bearer realm="trynet-test"', "auth challenge"
                        passed(f"origin {status} rejection and headers preserved")
                async with dial() as ws:
                    await ws.recv()
                    ws.transport.abort()
                async with dial() as ws:
                    await ws.recv()
                    await ws.send("__abort__")
                    try:
                        await ws.recv()
                        raise AssertionError("origin abort did not close client")
                    except ConnectionClosed:
                        pass
                async with dial() as ws:
                    await ws.recv()
                    await ws.send("reconnected")
                    assert await ws.recv() == "reconnected", "new connection recovery"
                for _ in range(50):
                    if not active:
                        break
                    await asyncio.sleep(0.1)
                assert not active, f"{len(active)} origin connections leaked"
                assert not failures, failures
                passed("abrupt disconnects, new connection recovery and origin socket cleanup")
                report["success"] = True
            finally:
                if process is not None and process.returncode is None:
                    process.terminate()
                    try:
                        await asyncio.wait_for(process.wait(), 10)
                    except TimeoutError:
                        process.kill()
                        await process.wait()
                if reader_task is not None:
                    await reader_task
                if not report["success"]:
                    report["tunnel_log_tail"] = "".join(output[-20:]).replace(token, "[redacted]")

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="./trynet")
    parser.add_argument("--output", default="websocket-smoke.json")
    parser.add_argument("--local-only", action="store_true", help="self-test the fixture without Cloudflare")
    args = parser.parse_args()
    report = {"success": False, "websockets": websockets.__version__, "checks": []}
    start = time.monotonic()
    try:
        asyncio.run(asyncio.wait_for(run_suite(args, report), 300))
    except Exception as exc:
        report["error"] = type(exc).__name__ + ": " + str(exc)
        report["traceback"] = traceback.format_exc()
        print(report["traceback"], file=sys.stderr)
    finally:
        report["elapsed_seconds"] = round(time.monotonic()-start, 2)
        Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    return 0 if report["success"] else 1

if __name__ == "__main__":
    sys.exit(main())
