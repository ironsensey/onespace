#!/usr/bin/env python3
"""Native Linux fixture for the Windows engine's hub transport, not real paste.

Run only on Linux with native Linux builds of both executables. The desktop
engine uses its Linux clipboard stub; physical Windows/Android remain untested.
"""
import argparse
import base64
import json
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

from hub_smoke import PinnedClient, available_port


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hub-bin", required=True, type=Path)
    parser.add_argument("--desktop-bin", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if not sys.platform.startswith("linux"):
        raise SystemExit("This fixture requires Linux clipboard stubs; do not run it against a real Windows desktop")
    checks = []
    result = {"status": "passed", "checks": checks,
              "limits": "Linux desktop-engine clipboard stub; transport, persistence and ACK only, not physical paste or phone behavior"}
    processes = []
    with tempfile.TemporaryDirectory(prefix="onespace-desktop-hub-") as temporary:
        directory = Path(temporary)
        data = directory / "hub"
        data.mkdir(mode=0o700)
        desktop = directory / "desktop"
        desktop.mkdir(mode=0o700)
        hub_bin = str(args.hub_bin.resolve())
        desktop_bin = str(args.desktop_bin.resolve())

        def admin(*arguments):
            response = subprocess.run([hub_bin, arguments[0], "--data", str(data), *arguments[1:]],
                                      capture_output=True, text=True)
            assert response.returncode == 0, "Fixture provisioning failed"
            return json.loads(response.stdout)

        pc = admin("enroll", "--id", "windows", "--name", "Fixture PC", "--kind", "windows", "--scopes", "clipboard")
        phone = admin("enroll", "--id", "phone", "--name", "Fixture phone", "--kind", "android", "--scopes", "clipboard")
        port = available_port()
        profile = {"address": f"https://localhost:{port}", "fingerprint": pc["fingerprint"],
                   "target": "phone", "enabled": True, "clipboardMode": "lan",
                   "receiveClipboard": True, "allowMedia": False, "overlayCIDRs": [],
                   # This is the documented non-Windows test adapter, not DPAPI.
                   "protectedToken": base64.b64encode(pc["token"].encode()).decode()}
        profile_file = desktop / "hub.json"
        profile_file.write_text(json.dumps(profile))
        profile_file.chmod(0o600)
        hub_log = (directory / "hub.log").open("w")
        desktop_log = (directory / "desktop.log").open("w")
        local = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        try:
            hub = subprocess.Popen([hub_bin, "serve", "--data", str(data), "--listen", f"127.0.0.1:{port}"],
                                   stdout=hub_log, stderr=hub_log)
            processes.append(hub)
            client = PinnedClient(port, data / "identity.crt", pc["fingerprint"])
            for _ in range(100):
                try:
                    client.call("/hub/v1/info")
                    break
                except OSError:
                    assert hub.poll() is None, "Hub fixture exited"
                    time.sleep(0.05)
            else:
                raise AssertionError("Hub readiness timeout")
            engine = subprocess.Popen([desktop_bin, "-no-browser", "-data", str(desktop)],
                                      stdout=desktop_log, stderr=desktop_log)
            processes.append(engine)
            for _ in range(100):
                try:
                    page = local.open("http://127.0.0.1:47600/", timeout=1).read().decode()
                    break
                except OSError:
                    assert engine.poll() is None, "Desktop fixture exited; check that ports 47600/47601 are free"
                    time.sleep(0.05)
            else:
                raise AssertionError("Desktop readiness timeout")
            key_match = re.search(r"const KEY='([a-f0-9]{48})'", page)
            assert key_match, "Local fixture authorization key not found"
            key = key_match.group(1)

            def local_call(path, method="GET", body=None, auth=True, expected=200):
                headers = {"Content-Type": "application/json"}
                if auth:
                    headers["X-Local-Key"] = key
                payload = json.dumps(body).encode() if body is not None else None
                request = urllib.request.Request("http://127.0.0.1:47600" + path, payload, headers, method=method)
                try:
                    response = local.open(request, timeout=6)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    assert response.status == expected, f"Local {path} returned {response.status}, expected {expected}"
                    return json.loads(response.read())

            local_call("/local/hub/config", auth=False, expected=403)
            config = local_call("/local/hub/config")
            assert config["hasToken"] and pc["token"] not in json.dumps(config)
            checks.append("desktop local hub settings require process key and redact credential")
            devices = local_call("/local/hub/devices")
            assert {d["id"] for d in devices} == {"windows", "phone"}
            checks.append("desktop proxy authenticates to separately pinned hub registry")

            outgoing = local_call("/local/hub/send", "POST",
                {"to": "phone", "kind": "clipboard.text", "text": "Desktop transport fixture", "action": ""}, expected=201)
            jobs, _ = client.call("/hub/v1/events", token=phone["token"])
            assert len(jobs) == 1 and jobs[0]["id"] == outgoing["id"] and jobs[0]["from"] == "windows"
            client.call(f"/hub/v1/events/{outgoing['id']}/ack", "POST", token=phone["token"], value={})
            checks.append("desktop UI send reaches correct enrolled phone queue with sender identity")

            incoming, _ = client.call("/hub/v1/events", "POST", token=phone["token"],
                value={"id": str(uuid.uuid4()), "to": "windows", "kind": "clipboard.text", "text": "Inbound fixture text"}, expected=201)
            receipt_path = desktop / "hub-receipts.json"
            deadline = time.monotonic() + 10
            receipt_key = pc["fingerprint"] + ":" + incoming["id"]
            while time.monotonic() < deadline:
                saved = json.loads(receipt_path.read_text()) if receipt_path.exists() else {}
                queued, _ = client.call("/hub/v1/events", token=pc["token"])
                if receipt_key in saved and not any(e["id"] == incoming["id"] for e in queued):
                    break
                time.sleep(0.05)
            else:
                raise AssertionError("Desktop did not persist receipt and ACK inbound event")
            checks.append("desktop background receiver commits receipt and ACKs inbound hub text using Linux clipboard stub")
        finally:
            for process in reversed(processes):
                if process.poll() is None:
                    process.send_signal(signal.SIGINT)
                    process.wait(timeout=10)
            hub_log.close()
            desktop_log.close()
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
