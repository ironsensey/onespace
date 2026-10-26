#!/usr/bin/env python3
"""Hermetic CLI/TLS/WHEP integration checks. No real camera or clipboard.

Usage: python3 tests/hub_smoke.py --hub-bin /path/to/native/onespace-hub
Credentials stay in memory and temporary owner-only files. Output contains
check names and timings only, never bearer tokens or clipboard payloads.
"""
import argparse
import concurrent.futures
import hashlib
import http.client
import http.server
import json
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import uuid


class PinMismatch(Exception):
    pass


class PinnedClient:
    def __init__(self, port, certificate, fingerprint):
        self.port = port
        self.context = ssl.create_default_context(cafile=str(certificate))
        self.fingerprint = fingerprint

    def call(self, path, method="GET", token="", value=None, raw=None,
             content_type="application/json", expected=200):
        connection = http.client.HTTPSConnection(
            "localhost", self.port, context=self.context, timeout=8)
        try:
            connection.connect()
            actual = hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest()
            if actual != self.fingerprint:
                raise PinMismatch("Hub certificate fingerprint mismatch")
            # Authentication is not sent until certificate pin verification.
            headers = {"Content-Type": content_type}
            if token:
                headers["Authorization"] = "Bearer " + token
            payload = raw if raw is not None else (
                json.dumps(value).encode() if value is not None else None)
            connection.request(method, path, body=payload, headers=headers)
            response = connection.getresponse()
            body = response.read()
            assert response.status == expected, (
                f"{method} {path} returned {response.status}; expected {expected}")
            result_headers = dict(response.getheaders())
            if response.getheader("Content-Type", "").startswith("application/json"):
                return json.loads(body), result_headers
            return body, result_headers
        finally:
            connection.close()


class FakeMediaMTX(http.server.BaseHTTPRequestHandler):
    """Signaling fixture only; it cannot produce a playable WebRTC stream."""
    sessions = set()
    deleted = []
    mode = "normal"
    guard = threading.Lock()

    def log_message(self, *_):
        pass

    def do_OPTIONS(self):
        self.send_response(204)
        self.end_headers()

    def do_POST(self):
        assert self.path == "/camera/whep"
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        assert not self.headers.get("Authorization"), "Hub credential leaked to media upstream"
        with self.guard:
            name = str(uuid.uuid4())
            self.sessions.add("/camera/whep/" + name)
            mode = self.mode
        if mode == "redirect":
            self.send_response(302)
            self.send_header("Location", "http://127.0.0.1:1/forbidden")
        else:
            self.send_response(201)
            self.send_header("Content-Type", "application/sdp")
            self.send_header("Location", "http://example.invalid/forbidden" if mode == "foreign" else "/camera/whep/" + name)
        self.end_headers()
        self.wfile.write(b"v=0\r\ns=signaling-fixture-only\r\n")

    def do_DELETE(self):
        with self.guard:
            self.deleted.append(self.path)
            self.sessions.discard(self.path)
        self.send_response(204)
        self.end_headers()


def available_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hub-bin", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--expiry", action="store_true", help="Also wait for a real media-event expiry and durable purge")
    args = parser.parse_args()
    binary = str(args.hub_bin.resolve())
    checks = []
    result = {"status": "passed", "checks": checks,
              "limits": "Local CLI/TLS/signaling fixtures only; no physical Pi, Windows clipboard, Android runtime, or video ICE test"}

    with tempfile.TemporaryDirectory(prefix="onespace-hub-smoke-") as temporary:
        directory = Path(temporary)
        data = directory / "data"
        data.mkdir(mode=0o700)

        def cli(*arguments):
            process = subprocess.run([binary, arguments[0], "--data", str(data), *arguments[1:]],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            assert process.returncode == 0, "Hub administrative command failed; inspect local fixture separately"
            return process.stdout.strip()

        credentials = {}
        for device, kind, scopes in [("sender", "windows", "clipboard,media"),
                                     ("phone", "android", "clipboard,media,camera"),
                                     ("viewer", "linux", "camera"),
                                     ("other", "android", "camera")]:
            credentials[device] = json.loads(cli("enroll", "--id", device,
                "--name", device, "--kind", kind, "--scopes", scopes))
        fingerprint = cli("fingerprint")
        assert len(fingerprint) == 64
        assert all(c["fingerprint"] == fingerprint for c in credentials.values())
        persisted = (data / "hub.json").read_text()
        assert all(c["token"] not in persisted for c in credentials.values())
        checks.append("local enrollment stores token hashes and stable certificate identity")

        upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FakeMediaMTX)
        upstream_thread = threading.Thread(target=upstream.serve_forever, daemon=True)
        upstream_thread.start()
        port = available_port()
        process = None
        log = (directory / "server.log").open("w")

        def start():
            nonlocal process
            process = subprocess.Popen([binary, "serve", "--data", str(data),
                "--listen", f"127.0.0.1:{port}", "--camera-upstream",
                f"http://127.0.0.1:{upstream.server_port}/camera/whep"],
                stdout=log, stderr=log)
            client = PinnedClient(port, data / "identity.crt", fingerprint)
            for _ in range(100):
                try:
                    info, _ = client.call("/hub/v1/info")
                    assert info["app"] == "OneSpace Hub" and info["recording"] is False
                    return client
                except (ConnectionRefusedError, OSError):
                    assert process.poll() is None, "Hub exited before readiness"
                    time.sleep(0.05)
            raise AssertionError("Hub failed readiness")

        def stop():
            nonlocal process
            if process is not None and process.poll() is None:
                process.send_signal(signal.SIGTERM)
                process.wait(timeout=10)
            process = None

        try:
            client = start()
            sender = credentials["sender"]["token"]
            phone = credentials["phone"]["token"]
            viewer = credentials["viewer"]["token"]
            other = credentials["other"]["token"]
            wrong_pin = PinnedClient(port, data / "identity.crt", "0" * 64)
            try:
                wrong_pin.call("/hub/v1/devices", token=sender)
                raise AssertionError("Incorrect certificate pin was accepted")
            except PinMismatch:
                pass
            try:
                untrusted = http.client.HTTPSConnection("localhost", port, timeout=4)
                untrusted.request("GET", "/hub/v1/info")
                raise AssertionError("Untrusted certificate was accepted")
            except ssl.SSLCertVerificationError:
                pass
            finally:
                untrusted.close()
            checks.append("TLS trust and exact pin reject untrusted identities before sending credentials")

            client.call("/hub/v1/devices", expected=401)
            client.call("/hub/v1/devices", token="invalid", expected=401)
            devices, _ = client.call("/hub/v1/devices", token=sender)
            assert len(devices) == 4
            assert all(c["token"] not in json.dumps(devices) for c in credentials.values())
            checks.append("authenticated registry excludes bearer credentials")

            request = {"id": str(uuid.uuid4()), "to": "phone", "kind": "clipboard.text", "text": "Fixture text: Unicode \u2713"}
            client.call("/hub/v1/events", "POST", token=viewer, value=request, expected=403)
            denied_target = dict(request, to="viewer")
            client.call("/hub/v1/events", "POST", token=sender, value=denied_target, expected=403)
            checks.append("clipboard scope is required on both sender and recipient")

            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
                waiting = executor.submit(client.call, "/hub/v1/events?wait=4", token=phone)
                time.sleep(0.1)
                begin = time.monotonic()
                event, _ = client.call("/hub/v1/events", "POST", token=sender, value=request, expected=201)
                jobs, _ = waiting.result(timeout=5)
                result["local_enqueue_to_poll_ms"] = round((time.monotonic() - begin) * 1000, 2)
            assert len(jobs) == 1 and jobs[0]["id"] == event["id"] and jobs[0]["text"] == request["text"]
            duplicate, _ = client.call("/hub/v1/events", "POST", token=sender, value=request)
            assert duplicate["id"] == event["id"]
            client.call("/hub/v1/events", "POST", token=sender, value=dict(request, text="Different fixture"), expected=409)
            client.call(f"/hub/v1/events/{event['id']}/ack", "POST", token=sender, value={}, expected=404)
            client.call(f"/hub/v1/events/{event['id']}/ack", "POST", token=phone, value={})
            client.call(f"/hub/v1/events/{event['id']}/ack", "POST", token=phone, value={})
            assert client.call("/hub/v1/events", token=phone)[0] == []
            duplicate, _ = client.call("/hub/v1/events", "POST", token=sender, value=request)
            assert duplicate["id"] == event["id"] and duplicate["duplicate"] is True
            checks.append("event-woken delivery, Unicode, idempotency, ACK ownership and duplicate ACK")

            client.call("/hub/v1/events", "POST", token=sender,
                value={"id": str(uuid.uuid4()), "to": "phone", "kind": "clipboard.text", "text": "x" * 65537}, expected=400)
            checks.append("oversized clipboard payload rejected")

            camera, _ = client.call("/hub/v1/camera/status", token=viewer)
            assert camera["configured"] and camera["reachable"] and camera["recording"] is False
            client.call("/hub/v1/camera/status", token=sender, expected=403)
            offer = b"v=0\r\ns=fixture-offer\r\n"
            answer, headers = client.call("/hub/v1/camera/whep", "POST", token=viewer,
                raw=offer, content_type="application/sdp", expected=201)
            assert answer.startswith(b"v=0")
            location = headers["Location"]
            assert location.startswith("/hub/v1/camera/session/")
            client.call(location, "DELETE", token=other, expected=404)
            client.call(location, "DELETE", token=viewer, expected=204)
            assert FakeMediaMTX.deleted
            client.call("/hub/v1/camera/whep", "POST", token=viewer,
                raw=b"v=0\r\n" + b"x" * (256 * 1024), content_type="application/sdp", expected=413)
            for mode in ("foreign", "redirect"):
                FakeMediaMTX.mode = mode
                client.call("/hub/v1/camera/whep", "POST", token=viewer,
                    raw=offer, content_type="application/sdp", expected=502)
            FakeMediaMTX.mode = "normal"
            checks.append("camera scope, fixed WHEP routing, session ownership, SDP bound and unsafe upstream responses")

            queued, _ = client.call("/hub/v1/events", "POST", token=sender,
                value=dict(request, id=str(uuid.uuid4())), expected=201)
            stop()
            client = start()
            jobs, _ = client.call("/hub/v1/events", token=phone)
            assert any(job["id"] == queued["id"] for job in jobs)
            checks.append("queued event and hub TLS identity survive service restart")

            if args.expiry:
                for job in jobs:
                    client.call(f"/hub/v1/events/{job['id']}/ack", "POST", token=phone, value={})
                media, _ = client.call("/hub/v1/events", "POST", token=sender,
                    value={"id": str(uuid.uuid4()), "to": "phone", "kind": "media", "action": "play_pause"}, expected=201)
                deadline = time.monotonic() + 23
                while time.monotonic() < deadline:
                    state = json.loads((data / "hub.json").read_text())
                    if not any(e["id"] == media["id"] for e in state["events"]):
                        break
                    time.sleep(0.25)
                else:
                    raise AssertionError("Expired media event not durably purged")
                assert client.call("/hub/v1/events", token=phone)[0] == []
                checks.append("idle media-event expiry is durably purged without recipient ACK")

            stop()
            cli("revoke", "--id", "sender")
            client = start()
            client.call("/hub/v1/devices", token=sender, expected=401)
            jobs, _ = client.call("/hub/v1/events", token=phone)
            assert not any(job["from"] == "sender" for job in jobs)
            checks.append("offline administrative revocation invalidates credentials and queued sender events")
        finally:
            stop()
            log.close()
            upstream.shutdown()
            upstream.server_close()

    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
