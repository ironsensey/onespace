//go:build windows

package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
)

var nativeCameraLock sync.Mutex

// Only the video surface uses the OS WebView2 media engine. Settings and all
// other dashboard controls are native Win32; no external browser is launched.
func openNativeCamera(a *App) error {
	if !nativeCameraLock.TryLock() {
		return errors.New("the camera viewer is already open")
	}
	defer nativeCameraLock.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := syscall.NewLazyDLL("ole32.dll")
	ole.NewProc("CoInitializeEx").Call(0, 2)
	defer ole.NewProc("CoUninitialize").Call()
	c := a.hub.snapshot()
	if !c.Enabled {
		return errors.New("configure and enable the Pi connection first")
	}
	cli, err := newPinnedHubClient(c)
	if err != nil {
		return err
	}
	defer cli.CloseIdleConnections()
	w := webview.NewWithOptions(webview.WebViewOptions{Configure: func(browser *edge.Chromium) {
		browser.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)
		browser.WebResourceRequestedCallback = func(_ *edge.ICoreWebView2WebResourceRequest, args *edge.ICoreWebView2WebResourceRequestedEventArgs) {
			response, err := browser.Environment().CreateWebResourceResponse(nil, 403, "Blocked", "")
			if err == nil {
				_ = args.PutResponse(response)
			}
		}
		browser.AddWebResourceRequestedFilter("*", edge.COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL)
	}, Debug: false, DataPath: filepath.Join(a.s.root, "CameraRuntime"), WindowOptions: webview.WindowOptions{Title: "OneSpace Live Camera", Width: 960, Height: 640, Center: true}})
	if w == nil {
		return errors.New("Microsoft WebView2 Runtime is required for the video surface")
	}
	defer w.Destroy()
	nonce := randomHex(32)
	var mu sync.Mutex
	session := ""
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup := func() {
		mu.Lock()
		path := session
		session = ""
		mu.Unlock()
		if path != "" {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if res, e := hubRequest(cleanupCtx, cli, c, "DELETE", path, "", nil); e == nil {
				res.Body.Close()
			}
		}
	}
	defer cleanup()
	if err = w.Bind("closeCamera", func(proof string) error {
		if subtle.ConstantTimeCompare([]byte(proof), []byte(nonce)) != 1 {
			return errors.New("viewer authorization required")
		}
		cleanup()
		return nil
	}); err != nil {
		return err
	}
	if err = w.Bind("openCamera", func(proof, sdp string) (string, error) {
		if subtle.ConstantTimeCompare([]byte(proof), []byte(nonce)) != 1 {
			return "", errors.New("viewer authorization required")
		}
		if len(sdp) > 256<<10 {
			return "", errors.New("camera offer is too large")
		}
		cleanup()
		requestCtx, stop := context.WithTimeout(ctx, 12*time.Second)
		defer stop()
		response, err := hubRequest(requestCtx, cli, c, "POST", "/hub/v1/camera/whep", "application/sdp", strings.NewReader(sdp))
		if err != nil {
			return "", errors.New("camera permission or connection failed")
		}
		defer response.Body.Close()
		path := response.Header.Get("Location")
		if !strings.HasPrefix(path, "/hub/v1/camera/session/") || strings.ContainsAny(path, "?#\\\r\n") || strings.Contains(path, "..") {
			return "", errors.New("invalid camera session")
		}
		mu.Lock()
		session = path
		mu.Unlock()
		body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10+1))
		if err != nil || len(body) > 256<<10 {
			cleanup()
			return "", errors.New("invalid camera answer")
		}
		return string(body), nil
	}); err != nil {
		return err
	}
	// No script bindings expose hub credentials, arbitrary network access or files.
	// The content contains no user-provided HTML or navigation links.
	w.SetHtml(strings.ReplaceAll(nativeCameraHTML, "__VIEWER_NONCE__", nonce))
	w.Run()
	return nil
}

const nativeCameraHTML = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; media-src blob:; connect-src 'none'; form-action 'none'; base-uri 'none'"><style>body{background:#101820;color:#eee;font:16px system-ui;margin:16px}video{width:100%;height:75vh;background:#000}button{padding:10px;margin:8px}</style></head><body><video id="v" autoplay playsinline controls></video><p id="s">Ready. Recording is controlled separately.</p><button id="start">Connect</button><button id="stop">Disconnect</button><script>
const credential='__VIEWER_NONCE__';let peer=null,generation=0;const state=document.getElementById('s');
async function stop(){generation++;if(peer){peer.close();peer=null}document.getElementById('v').srcObject=null;await closeCamera(credential);state.textContent='Disconnected'}
document.getElementById('stop').onclick=()=>stop().catch(()=>{});
document.getElementById('start').onclick=async()=>{await stop();const stamp=generation;try{const p=new RTCPeerConnection({iceServers:[]});peer=p;p.addTransceiver('video',{direction:'recvonly'});p.ontrack=e=>{if(stamp===generation)document.getElementById('v').srcObject=e.streams[0]||new MediaStream([e.track])};p.onconnectionstatechange=()=>{if(stamp===generation)state.textContent='Video connection: '+p.connectionState};await p.setLocalDescription(await p.createOffer());await new Promise((resolve,reject)=>{if(p.iceGatheringState==='complete')return resolve();const timer=setTimeout(()=>reject(Error('Network negotiation timed out')),6000);p.onicegatheringstatechange=()=>{if(p.iceGatheringState==='complete'){clearTimeout(timer);resolve()}}});if(stamp!==generation)return;const answer=await openCamera(credential,p.localDescription.sdp);if(stamp!==generation){await closeCamera(credential);return}await p.setRemoteDescription({type:'answer',sdp:answer})}catch(e){await stop();state.textContent=e.message}};
document.addEventListener('visibilitychange',()=>{if(document.hidden)stop().catch(()=>{})});window.addEventListener('pagehide',()=>{if(peer)peer.close();closeCamera(credential).catch(()=>{})});
</script></body></html>`
