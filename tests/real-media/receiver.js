const { COORDINATOR, VIEWER_TOKEN, VIEWER_ID, TARGET_ID, TURN_URL, TURN_USERNAME, TURN_CREDENTIAL, FORCE_RELAY } = process.env;

const { chromium } = require('playwright');

class BrowserDecodedFrameReporter {
  constructor() {
    this.frames = 0;
    this.candidatePair = {};
  }

  async run() {
    console.log('Launching Chromium with Playwright for WebRTC testing...');
    console.log('Environment:', {
      coordinator: COORDINATOR,
      viewer: VIEWER_ID,
      target: TARGET_ID,
      turn: TURN_URL,
      forceRelay: FORCE_RELAY
    });

    const browser = await chromium.launch({
      headless: true,
      args: [
        '--use-fake-device-for-media-stream',
        '--use-fake-ui-for-media-stream',
        '--disable-background-networking',
        '--disable-component-extensions-with-background-pages',
        '--disable-default-apps',
        '--disable-dev-shm-usage',
        '--disable-extensions',
        '--disable-background-timer-throttling',
        '--disable-renderer-backgrounding',
        '--disable-background-media-render-inactive',
        '--mute-audio',
        '--disable-notifications',
        '--no-first-run',
        '--password-store=basic',
        '--disable-speech-api',
        '--disable-features=VizDisplayCompositor'
      ]
    });

    try {
      const context = await browser.newContext({
        ignoreHTTPSErrors: true
      });

      const page = await context.newPage();
      await page.goto('about:blank');

      const config = {
        coordinator: COORDINATOR,
        viewerToken: VIEWER_TOKEN,
        viewerId: VIEWER_ID,
        targetId: TARGET_ID,
        turnUrl: TURN_URL,
        turnUsername: TURN_USERNAME,
        turnCredential: TURN_CREDENTIAL,
        forceRelay: FORCE_RELAY === '1'
      };

      const result = await page.evaluate(async (config) => {
        const pc = new RTCPeerConnection({
          iceServers: config.turnUrl ? [{
            urls: config.turnUrl,
            username: config.turnUsername,
            credential: config.turnCredential
          }] : [],
          iceTransportPolicy: config.forceRelay ? 'relay' : undefined
        });

        pc.addTransceiver('video', { direction: 'recvonly' });

        const videoElement = document.createElement('video');
        videoElement.muted = true;
        videoElement.playsInline = true;
        videoElement.autoplay = true;
        videoElement.style.display = 'none';
        document.body.appendChild(videoElement);

        let frames = 0;
        let candidatePair = {};
        let connectionState = 'new';
        let iceGatheringState = 'new';
        let videoWidth = 0;
        let videoHeight = 0;
        let isPlaying = false;
        let lastCurrentTime = 0;
        let frameCountFromCallback = 0;

        pc.onicecandidate = (event) => {
          if (event.candidate) {
            console.log('ICE candidate:', event.candidate.candidateType, event.candidate.candidate);
          } else {
            console.log('ICE gathering complete');
          }
        };

        pc.onicegatheringstatechange = () => {
          iceGatheringState = pc.iceGatheringState;
          console.log('ICE gathering state:', iceGatheringState);
        };

        pc.onconnectionstatechange = () => {
          connectionState = pc.connectionState;
          console.log('Connection state:', connectionState);
        };

        pc.oniceconnectionstatechange = () => {
          console.log('ICE connection state:', pc.iceConnectionState);
        };

        pc.ontrack = (track) => {
          console.log('Received track:', track.kind, track.id);
          if (track.kind === 'video') {
            videoElement.srcObject = new MediaStream([track]);
            videoElement.onloadedmetadata = () => {
              videoWidth = videoElement.videoWidth;
              videoHeight = videoElement.videoHeight;
              console.log('Video dimensions:', videoWidth, 'x', videoHeight);
              videoElement.play().then(() => {
                isPlaying = true;
                console.log('Video playing');
              }).catch(e => console.log('Play failed:', e));
            };

            // Use requestVideoFrameCallback if available, otherwise fallback to time-based counting
            if ('requestVideoFrameCallback' in videoElement) {
              const countFrames = (now, metadata) => {
                if (videoElement.readyState >= 2) {
                  frameCountFromCallback++;
                  console.log('Video frame callback:', frameCountFromCallback, 'currentTime:', videoElement.currentTime);
                }
                videoElement.requestVideoFrameCallback(countFrames);
              };
              videoElement.requestVideoFrameCallback(countFrames);
            } else {
              // Fallback: count based on currentTime changes
              const checkFrames = () => {
                if (videoElement.currentTime > lastCurrentTime && videoElement.readyState >= 2) {
                  frames++;
                  lastCurrentTime = videoElement.currentTime;
                  console.log('Frame detected via time:', frames);
                }
                setTimeout(checkFrames, 100);
              };
              checkFrames();
            }
          }
        };

        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        console.log('Local description set');

        const iceDeadline = Date.now() + 6000;
        while (pc.iceGatheringState !== 'complete' && Date.now() < iceDeadline) {
          await new Promise(resolve => setTimeout(resolve, 50));
        }
        console.log('ICE gathering finished:', pc.iceGatheringState);

        const sessionResponse = await fetch(config.coordinator + '/v1/coord/sessions', {
          method: 'POST',
          headers: {
            'Authorization': 'Bearer ' + config.viewerToken,
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({ viewer: config.viewerId, target: config.targetId })
        });

        if (!sessionResponse.ok) {
          const errorText = await sessionResponse.text();
          throw new Error('Failed to create session: ' + sessionResponse.status + ' ' + errorText);
        }

        const sessionData = await sessionResponse.json();
        const sessionId = sessionData.session;
        console.log('Session created:', sessionId);

        const offerResponse = await fetch(config.coordinator + '/v1/coord/sessions/' + sessionId + '/messages?from=' + config.viewerId, {
          method: 'POST',
          headers: {
            'Authorization': 'Bearer ' + config.viewerToken,
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({ type: 'offer', body: pc.localDescription.sdp })
        });

        if (!offerResponse.ok) {
          const errorText = await offerResponse.text();
          throw new Error('Failed to send offer: ' + offerResponse.status + ' ' + errorText);
        }
        console.log('Offer sent');

        let answerReceived = false;
        const pollDeadline = Date.now() + 30000;
        while (!answerReceived && Date.now() < pollDeadline) {
          const pollResponse = await fetch(config.coordinator + '/v1/coord/sessions/' + sessionId + '/poll?for=' + config.viewerId, {
            headers: { 'Authorization': 'Bearer ' + config.viewerToken }
          });

          if (pollResponse.ok) {
            const messages = await pollResponse.json();
            for (const msg of messages) {
              if (msg.type === 'answer') {
                await pc.setRemoteDescription(new RTCSessionDescription({ type: 'answer', sdp: msg.body }));
                answerReceived = true;
                console.log('Answer received and set');
                break;
              }
            }
          }
          await new Promise(resolve => setTimeout(resolve, 500));
        }

        if (!answerReceived) {
          throw new Error('No answer received from coordinator');
        }

        const frameDeadline = Date.now() + 15000;
        while (frameCountFromCallback < 1 && frames < 1 && Date.now() < frameDeadline) {
          await new Promise(resolve => setTimeout(resolve, 100));
        }

        const stats = await pc.getStats();
        const localCandidates = [];
        const remoteCandidates = [];

        const statsReports = stats instanceof Map ? Array.from(stats.values()) : Object.values(stats);
        for (const report of statsReports) {
          if (report.type === 'local-candidate') {
            localCandidates.push({
              id: report.id,
              type: report.candidateType,
              priority: report.priority || 0
            });
          } else if (report.type === 'remote-candidate') {
            remoteCandidates.push({
              id: report.id,
              type: report.candidateType,
              priority: report.priority || 0
            });
          } else if (report.type === 'candidate-pair') {
            if (report.state === 'succeeded') {
              candidatePair = {
                local: report.localCandidateId || '',
                remote: report.remoteCandidateId || '',
                localType: localCandidates.find(c => c.id === report.localCandidateId)?.type || '',
                remoteType: remoteCandidates.find(c => c.id === report.remoteCandidateId)?.type || '',
                state: report.state
              };
              console.log('Selected candidate pair:', candidatePair);
            }
          }
        }

        // Use frameCountFromCallback if available, otherwise use frames
        const totalFrames = frameCountFromCallback > 0 ? frameCountFromCallback : frames;

        try {
          await fetch(config.coordinator + '/v1/coord/sessions/' + sessionId + '/close?by=' + config.viewerId, {
            method: 'DELETE',
            headers: { 'Authorization': 'Bearer ' + config.viewerToken }
          });
        } catch (e) {
          console.log('Session close error:', e);
        }

        if (totalFrames < 1) {
          throw new Error('No decoded video frames received (counted: ' + totalFrames + ')');
        }

        if (videoWidth <= 0 || videoHeight <= 0) {
          throw new Error('Invalid video dimensions: ' + videoWidth + 'x' + videoHeight);
        }

        if (!isPlaying) {
          throw new Error('Video not playing');
        }

        if (config.forceRelay) {
          const selectedLocal = localCandidates.find(c => c.id === candidatePair.local);
          if (!selectedLocal || selectedLocal.type !== 'relay') {
            throw new Error('Forced relay did not select a relay pair: ' + JSON.stringify(candidatePair) + '; local: ' + JSON.stringify(selectedLocal));
          }
        }

        return {
          decodedFrames: totalFrames,
          candidatePair: candidatePair,
          videoWidth: videoWidth,
          videoHeight: videoHeight,
          isPlaying: isPlaying
        };

      }, config);

      const output = {
        decodedFrames: result.decodedFrames,
        candidatePair: result.candidatePair,
        videoWidth: result.videoWidth,
        videoHeight: result.videoHeight,
        isPlaying: result.isPlaying
      };

      console.log('Test completed successfully');
      console.log('Final result:', JSON.stringify(output));

      return output;

    } catch (error) {
      console.error('Browser test failed:', error);
      throw error;
    } finally {
      await browser.close();
    }
  }
}

async function main() {
  try {
    const reporter = new BrowserDecodedFrameReporter();
    const result = await reporter.run();
    console.log(JSON.stringify(result));
  } catch (error) {
    console.error('Node.js orchestrator failed:', error);
    process.exit(1);
  }
}

main().catch((error) => {
  console.error('Receiver startup failed:', error);
  process.exit(1);
});
EOF