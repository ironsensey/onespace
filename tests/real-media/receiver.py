import asyncio
import json
import os
import time

from aiohttp import ClientSession
from aiortc import RTCPeerConnection, RTCConfiguration, RTCIceServer, RTCSessionDescription
from aiortc.contrib.media import MediaBlackhole

BASE = os.environ["COORDINATOR"].rstrip("/")
VIEWER_TOKEN = os.environ["VIEWER_TOKEN"]
VIEWER = os.environ["VIEWER_ID"]
TARGET = os.environ["TARGET_ID"]


async def request(session, method, path, body=None):
    headers = {"Authorization": f"Bearer {VIEWER_TOKEN}"}
    async with session.request(method, BASE + path, headers=headers, json=body, ssl=False) as response:
        text = await response.text()
        if response.status >= 300:
            raise RuntimeError(f"{method} {path}: {response.status} {text}")
        return response, text


async def main():
    ice = []
    turn = os.environ.get("TURN_URL")
    if turn:
        ice.append(
            RTCIceServer(
                urls=turn,
                username=os.environ.get("TURN_USERNAME"),
                credential=os.environ.get("TURN_CREDENTIAL"),
            )
        )
    pc = RTCPeerConnection(
        RTCConfiguration(iceServers=ice),
    )
    frames = 0
    candidate_pair = {}
    blackhole = MediaBlackhole()

    @pc.on("track")
    async def on_track(track):
        nonlocal frames
        if track.kind != "video":
            return
        for _ in range(10):
            frame = await asyncio.wait_for(track.recv(), timeout=10)
            if frame.width and frame.height:
                frames += 1
                await blackhole.start()
                return

    pc.addTransceiver("video", direction="recvonly")
    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    deadline = time.monotonic() + 6
    while pc.iceGatheringState != "complete" and time.monotonic() < deadline:
        await asyncio.sleep(0.05)
    if pc.iceGatheringState != "complete":
        raise RuntimeError("ICE gathering did not complete")

    async with ClientSession() as session:
        _, text = await request(
            session,
            "POST",
            "/v1/coord/sessions",
            {"viewer": VIEWER, "target": TARGET},
        )
        session_id = json.loads(text)["session"]
        try:
            await request(
                session,
                "POST",
                f"/v1/coord/sessions/{session_id}/messages?from={VIEWER}",
                {"type": "offer", "body": pc.localDescription.sdp},
            )
            _, text = await request(
                session, "GET", f"/v1/coord/sessions/{session_id}/poll?for={VIEWER}"
            )
            messages = json.loads(text)
            answer = next(message["body"] for message in messages if message["type"] == "answer")
            await pc.setRemoteDescription(RTCSessionDescription(answer, "answer"))
            await asyncio.sleep(10)
            stats = await pc.getStats()
            candidates = {
                report.id: report
                for report in stats.values()
                if report.type == "local-candidate" or report.type == "remote-candidate"
            }
            selected_pair_id = ""
            for report in stats.values():
                if report.type == "transport":
                    selected_pair_id = getattr(report, "selectedCandidatePairId", "") or ""
            for report in stats.values():
                if (
                    report.type == "candidate-pair"
                    and (
                        report.id == selected_pair_id
                        or getattr(report, "state", "") == "succeeded"
                    )
                ):
                    local = candidates.get(getattr(report, "localCandidateId", ""))
                    remote = candidates.get(getattr(report, "remoteCandidateId", ""))
                    candidate_pair = {
                        "local": getattr(report, "localCandidateId", ""),
                        "remote": getattr(report, "remoteCandidateId", ""),
                        "localType": getattr(local, "candidateType", ""),
                        "remoteType": getattr(remote, "candidateType", ""),
                        "state": report.state,
                    }
                    if report.id == selected_pair_id:
                        break
            if frames < 1:
                raise RuntimeError("SDP succeeded but no decoded video frame arrived")
            if os.environ.get("FORCE_RELAY") == "1" and (
                candidate_pair.get("localType") != "relay"
                and candidate_pair.get("remoteType") != "relay"
            ):
                report_types = sorted({report.type for report in stats.values()})
                raise RuntimeError(
                    f"forced relay did not select a relay pair: "
                    f"{candidate_pair}; stats={report_types}"
                )
            print(json.dumps({"decodedFrames": frames, "candidatePair": candidate_pair}))
        finally:
            await request(session, "DELETE", f"/v1/coord/sessions/{session_id}/close?by={VIEWER}")
    await pc.close()


if __name__ == "__main__":
    asyncio.run(main())
