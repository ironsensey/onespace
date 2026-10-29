const { COORDINATOR, VIEWER_TOKEN, VIEWER_ID, TARGET_ID, TURN_URL, TURN_USERNAME, TURN_CREDENTIAL, FORCE_RELAY } = process.env;

const { chromium } = require('playwright');
const fs = require('fs');

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
        '--disable-features=MediaEngagementSignals', 
        '--disable-background-networking',
        '--disable-component-extensions-with-background-pages',
        '--disable-default-apps',
        '--disable-dev-shm-usage',
        '--disable-extensions',
        '--disable-features=TranslateUI',
        '--disable-ipc-flooding-protection',
        '--disable-rpc-warnings',
        '--disable-default-apps',
        '--disable-background-timer-throttling',
        '--disable-renderer-backgrounding',
        '--disable-background-media-render-inactive',
        '--mute-audio',
        '--disable-notifications',
        '--no-first-run',
        '--password-store=basic',
        '--use-mock-keychain',
        '--disable-speech-api',
        '--disable-web-security',
        '--allow-running-insecure-content',
        '--disable-features=VizDisplayCompositor'
      ]
    });

    try {
      const context = await browser.newContext({
        userAgent: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
        ignoreHTTPSErrors: true,
      });

      const page = await context.newPage();

      console.log('Browser launched, navigating to test page...');

      await page.goto('about:blank');

      const result = await page.evaluate(async () => {
        const environment = {
          coordinator: window.location.origin.includes('localhost') ? 'http://coordinator:47604' : window.location.origin,
          viewerToken: '${VIEWER_TOKEN}',
          viewerId: '${VIEWER_ID}',
          targetId: '${TARGET_ID}',
          turnUrl: '${TURN_URL}',
          turnUsername: '${TURN_USERNAME}',
          turnCredential: '${TURN_CREDENTIAL}',
          forceRelay: '${FORCE_RELAY}'
        };

        console.log('Test environment:', environment);

        const pc = new RTCPeerConnection({
          iceServers: [],
          iceTransportPolicy: environment.forceRelay === '1' ? 'relay' : undefined
        });

        if (environment.turnUrl) {
          pc.iceServers.push({
            urls: environment.turnUrl,
            username: environment.turnUsername,
            credential: environment.turnCredential
          });
        }

        let frames = 0;
        let candidatePair = {};
        let connectionState = 'new';
        let iceGatheringState = 'new';

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
            const videoElement = document.createElement('video');
            videoElement.srcObject = new MediaStream([track]);
            document.body.appendChild(videoElement);
            
            videoElement.onloadedmetadata = () => {
              videoElement.play().catch(e => console.log('Play failed:', e));
            };
            
            track.onreceive = (frame) => {
              if (frame.width && frame.height) {
                frames++;
                console.log('Decoded video frame:', frames, frame.width, 'x', frame.height);
              }
            };
          }
        };

        try {
          const offer = await pc.createOffer();
          await pc.setLocalDescription(offer);
          console.log('Local description set');
          
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
            if (pc.iceGatheringState === 'complete') {
              console.log('ICE gathering completed with', iceCandidates.length, 'local candidates');
            }
          };
          
          await new Promise(resolve => setTimeout(resolve, 1000));
          
          if (connectionState !== 'connected') {
            console.log('Connection not established, status:', connectionState);
          }
          
          const stats = await pc.getStats();
          const localCandidates = [];
          const remoteCandidates = [];
          
          stats.forEach(report => {
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
          });
          
          console.log('Stats collected: local candidates:', localCandidates.length, 'remote candidates:', remoteCandidates.length);
          
          if (frames < 1) {
            throw new Error('No decoded video frames received');
          }
          
          if (environment.forceRelay === '1') {
            const selectedLocal = localCandidates.find(c => c.id === candidatePair.local);
            const selectedRemote = remoteCandidates.find(c => c.id === candidatePair.remote);
            
            if (!selectedLocal || selectedLocal.type !== 'relay' || 
                !selectedRemote || selectedRemote.type !== 'relay') {
              throw new Error('Forced relay did not select a relay pair: ' + JSON.stringify(candidatePair) + '; local: ' + JSON.stringify(selectedLocal) + '; remote: ' + JSON.stringify(selectedRemote));
            }
          }
          
          return {
            decodedFrames: frames,
            candidatePair: candidatePair,
            localCandidates: localCandidates,
            remoteCandidates: remoteCandidates
          };
          
        } catch (error) {
          console.error('Browser evaluation error:', error);
          throw error;
        }
      });

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
