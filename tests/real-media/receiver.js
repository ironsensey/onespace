const { chromium } = require("playwright");

(async () => {
  const base = process.env.COORDINATOR;
  const viewerToken = process.env.VIEWER_TOKEN;
  const viewer = process.env.VIEWER_ID;
  const target = process.env.TARGET_ID;
  const turnUrl = process.env.TURN_URL || "";
  const turnUsername = process.env.TURN_USERNAME || "";
  const turnCredential = process.env.TURN_CREDENTIAL || "";
  const forceRelay = process.env.FORCE_RELAY === "1";

  const iceServers = turnUrl
    ? [{ urls: turnUrl, username: turnUsername, credential: turnCredential }]
    : [];

  // Coordinator HTTP goes through Playwright's request context (Node side), not
  // fetch() inside the page: the page is about:blank and the coordinator does not
  // emit CORS headers, so page-side fetch would be blocked. WebRTC itself must
  // run in the page, so the peer connection is bridged via window.__onespacePc.
  const browser = await chromium.launch({
    headless: true,
    args: ["--ignore-certificate-errors", "--no-sandbox", "--disable-setuid-sandbox"],
  });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();

  async function request(method, path, body) {
    const res = await context.request.fetch(base + path, {
      method,
      headers: {
        Authorization: "Bearer " + viewerToken,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text().catch(() => "");
    if (!res.ok) {
      throw new Error(method + " " + path + ": " + res.status + " " + text);
    }
    return text;
  }

  let sessionId = null;
  try {
    // Bounded readiness check: retry session creation until the coordinator is up.
    // Replaces the fixed sleep 8 with a deadline-bounded poll.
    let readyErr = null;
    const readyDeadline = Date.now() + 30000;
    while (Date.now() < readyDeadline) {
      try {
        const text = await request("POST", "/v1/coord/sessions", {
          viewer,
          target,
        });
        sessionId = JSON.parse(text).session;
        break;
      } catch (e) {
        readyErr = e;
        await new Promise((r) => setTimeout(r, 500));
      }
    }
    if (!sessionId) {
      throw new Error(
        "coordinator not reachable within readiness deadline: " + readyErr
      );
    }

    // Phase 1: create the offer in Chromium (WebRTC lives in the page).
    const offerSdp = await page.evaluate(async (ctx) => {
      const { iceServers, forceRelay } = ctx;
      const pc = new RTCPeerConnection({
        iceServers,
        iceTransportPolicy: forceRelay ? "relay" : "all",
      });
      window.__onespacePc = pc;
      let gathered = false;
      pc.addEventListener("icegatheringstatechange", () => {
        if (pc.iceGatheringState === "complete") gathered = true;
      });
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      const deadline = Date.now() + 8000;
      while (!gathered && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 50));
      }
      if (!gathered) {
        throw new Error("ICE gathering did not complete");
      }
      return pc.localDescription.sdp;
    }, { iceServers, forceRelay });

    // Phase 2: exchange the complete offer/answer through the authorized coordinator.
    await request(
      "POST",
      `/v1/coord/sessions/${sessionId}/messages?from=${viewer}`,
      { type: "offer", body: offerSdp }
    );
    const messagesText = await request(
      "GET",
      `/v1/coord/sessions/${sessionId}/poll?for=${viewer}`
    );
    const messages = JSON.parse(messagesText);
    const answer = messages.find((m) => m.type === "answer").body;

    // Phase 3: apply the answer and wait for a decoded frame plus the selected pair.
    const result = await page.evaluate(
      async (answerSdp, ctx) => {
        const { forceRelay } = ctx;
        const pc = window.__onespacePc;
        if (!pc) throw new Error("peer connection not found");

        let decodedFrames = 0;
        let frameReady = false;
        let selectedPair = null;
        let iceConnected = false;
        let videoEl = null;

        pc.addEventListener("iceconnectionstatechange", () => {
          if (pc.iceConnectionState === "connected") iceConnected = true;
        });

        pc.addEventListener("track", (e) => {
          if (e.track.kind !== "video") return;
          const video = document.createElement("video");
          video.autoplay = true;
          video.muted = true;
          video.playsInline = true;
          video.srcObject = new MediaStream([e.track]);
          videoEl = video;
          const mark = () => {
            if (!frameReady && video.videoWidth && video.videoHeight) {
              decodedFrames = 1;
              frameReady = true;
            }
          };
          video.addEventListener("loadedmetadata", mark);
          video.addEventListener("playing", mark);
          document.body.appendChild(video);
          video.play().catch(() => {});
        });

        await pc.setRemoteDescription(
          new RTCSessionDescription({ sdp: answerSdp, type: "answer" })
        );

        const deadline = Date.now() + 20000;
        while (Date.now() < deadline) {
          if (
            !frameReady &&
            videoEl &&
            videoEl.videoWidth &&
            videoEl.videoHeight
          ) {
            decodedFrames = 1;
            frameReady = true;
          }
          const t = pc.getTransceivers()[0];
          if (
            t &&
            t.receiver &&
            t.receiver.transport &&
            t.receiver.transport.iceTransport &&
            !selectedPair
          ) {
            const pair =
              t.receiver.transport.iceTransport.getSelectedCandidatePair();
            if (pair) {
              selectedPair = { local: pair.local.type, remote: pair.remote.type };
            }
          }
          if (frameReady && selectedPair && iceConnected) break;
          await new Promise((r) => setTimeout(r, 100));
        }

        if (decodedFrames < 1) {
          throw new Error("SDP succeeded but no decoded video frame arrived");
        }
        if (!iceConnected) {
          throw new Error("ICE did not connect");
        }
        // Viewer-only forced relay: the viewer's LOCAL candidate must be relay.
        // MediaMTX is not configured for relay, so the remote side is host/srflx;
        // requiring remote==relay would incorrectly fail a viewer-only TURN path.
        if (forceRelay) {
          if (!selectedPair || selectedPair.local.type !== "relay") {
            throw new Error(
              "forced relay did not select a relay pair: " +
                JSON.stringify(selectedPair)
            );
          }
        }

        return {
          decodedFrames,
          candidatePair: selectedPair || null,
          iceTransportPolicy: forceRelay ? "relay" : "all",
        };
      },
      answer,
      { forceRelay }
    );

    console.log(JSON.stringify(result));
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  } finally {
    try {
      if (sessionId) {
        await request(
          "DELETE",
          `/v1/coord/sessions/${sessionId}/close?by=${viewer}`
        );
      }
    } catch (e) {}
    try {
      await page.evaluate(() => {
        const pc = window.__onespacePc;
        if (pc) pc.close();
      });
    } catch (e) {}
    await context.close();
    await browser.close();
  }
})();
