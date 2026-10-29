const { COORDINATOR, VIEWER_TOKEN, VIEWER_ID, TARGET_ID, TURN_URL, TURN_USERNAME, TURN_CREDENTIAL, FORCE_RELAY } = process.env;

const { chromium } = require('playwright');

class BrowserDecodedFrameReporter {
  constructor() {
    this.frames = 0;
    this.candidatePair = {};
    this.videoElement = null;
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
        '--disable-web-security',
        '--allow-running-insecure-content',
        '--disable-features=VizDisplayCompositor'
      ]
    });

    try {
      const context = await browser.newContext();
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
          iceServers: [],
          iceTransportPolicy: config.forceRelay ? 'relay' : undefined
        });

        if (config.turnUrl) {
          pc.iceServers.push({
            urls: config.turnUrl,
            username: config.turnUsername,
            credential: config.turnCredential
          });
        }

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
        const iceCandidates = [];
        const remoteCandidates = [];

        pc.onicecandidate = (event) => {
          if (event.candidate) {
            iceCandidates.push({
              id: event.candidate.candidate,
              type: event.candidate.candidateType
            });
          }
        };

        pc.onicecandidateerror = (event) => {
          console.log('ICE candidate error:', event.errorCode, event.errorText);
        };

        pc.onicegatheringstatechange = () => {
          iceGatheringState = pc.iceGatheringState;
        };

        pc.onconnectionstatechange = () => {
          connectionState = pc.connectionState;
        };

        pc.ontrack = (track) => {
          console.log('Received track:', track.kind, track.id);
          if (track.kind === 'video') {
            videoElement.srcObject = new MediaStream([track]);
            videoElement.onloadedmetadata = () => {
              videoElement.play().catch(e => console.log('Play failed:', e));
            };
          }
        };

        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        console.log('Local description set');

        await new Promise(resolve => setTimeout(resolve, 1000));

        const stats = await pc.getStats();
        const localCandidates = [];
        const remoteCandidatesFinal = [];

        const statsReports = stats instanceof Array ? stats : Object.values(stats);
        for (const report of statsReports) {
          if (report.type === 'local-candidate') {
            localCandidates.push({
              id: report.id,
              type: report.candidateType,
              priority: report.priority || 0
            });
          } else if (report.type === 'remote-candidate') {
            remoteCandidatesFinal.push({
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
                remoteType: remoteCandidatesFinal.find(c => c.id === report.remoteCandidateId)?.type || '',
                state: report.state
              };
              console.log('Selected candidate pair:', candidatePair);
            }
          }
        }

        if (frames < 1) {
          throw new Error('No decoded video frames received');
        }

        if (config.forceRelay) {
          const selectedLocal = localCandidates.find(c => c.id === candidatePair.local);
          if (!selectedLocal || selectedLocal.type !== 'relay') {
            throw new Error('Forced relay did not select a relay pair: ' + JSON.stringify(candidatePair));
          }
        }

        return {
          decodedFrames: frames,
          candidatePair: candidatePair,
          localCandidates: localCandidates,
          remoteCandidates: remoteCandidatesFinal
        };
      }, config);

      const output = {
        decodedFrames: result.decodedFrames,
        candidatePair: result.candidatePair
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
