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

  const browser = await chromium.launch({
    headless: true,
    args: ["--ignore-certificate-errors", "--no-sandbox", "--disable-setuid-sandbox"],
  });
  const page = await browser.newPage();

  try {
    const result = await page.evaluate(
      async (ctx) => {
        const {
          base,
          viewerToken,
          viewer,
          target,
          turnUrl,
          turnUsername,
          turnCredential,
          forceRelay,
        } = ctx;

        async function request(method, path, body) {
          const res = await fetch(base + path, {
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

        const iceServers = turnUrl
          ? [{ urls: turnUrl, username: turnUsername, credential: turnCredential }]
          : [];
        const pc = new RTCPeerConnection({
          iceServers,
          iceTransportPolicy: forceRelay ? "relay" : "all",
        });

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

        pc.addTransceiver("video", { direction: "recvonly" });
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        const gatherDeadline = Date.now() + 8000;
        while (pc.iceGatheringState !== "complete" && Date.now() < gatherDeadline) {
          await new Promise((r) => setTimeout(r, 50));
        }
        if (pc.iceGatheringState !== "complete") {
          throw new Error("ICE gathering did not complete");
        }

        const sessionText = await request("POST", "/v1/coord/sessions", {
          viewer,
          target,
        });
      const sessionId = JSON.parse(sessionText).session;
      try {
        await request(
          "POST",
          `/v1/coord/sessions/${sessionId}/messages?from=${viewer}`,
          { type: "offer", body: pc.localDescription.sdp }
        );
        const messagesText = await request(
          "GET",
          `/v1/coord/sessions/${sessionId}/poll?for=${viewer}`
        );
        const messages = JSON.parse(messagesText);
        const answer = messages.find((m) => m.type === "answer").body;
        await pc.setRemoteDescription(
          new RTCSessionDescription({ sdp: answer, type: "answer" })
        );

        const deadline = Date.now() + 20000;
        while (Date.now() < deadline) {
          if (!frameReady && videoEl && videoEl.videoWidth && videoEl.videoHeight) {
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
            const pair = t.receiver.transport.iceTransport.getSelectedCandidatePair();
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
        if (forceRelay) {
          if (
            !selectedPair ||
            selectedPair.local.type !== "relay" ||
            selectedPair.remote.type !== "relay"
          ) {
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
      } finally {
        try {
          await request(
            "DELETE",
            `/v1/coord/sessions/${sessionId}/close?by=${viewer}`
          );
        } catch (e) {}
      }
      },
      {
        base,
        viewerToken,
        viewer,
        target,
        turnUrl,
        turnUsername,
        turnCredential,
        forceRelay,
      }
    );

    console.log(JSON.stringify(result));
  } finally {
    await browser.close();
  }
})();
