package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional real MediaMTX/FFmpeg integration. Google remains a local fixture;
// this proves actual finalized H.264 MP4 segments, not physical CSI capture.
func TestRealMediaMTXRecording(t *testing.T) {
	bin, hub := os.Getenv("ONESPACE_MEDIAMTX_BIN"), os.Getenv("ONESPACE_HUB_BIN")
	if bin == "" || hub == "" {
		t.Skip("set ONESPACE_MEDIAMTX_BIN and ONESPACE_HUB_BIN for real recording integration")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg required")
	}
	m, g := fixture(t)
	port := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	apiPort, rtspPort := port(), port()
	m.mediaAPI = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	config := fmt.Sprintf(`logLevel: warn
authMethod: internal
authInternalUsers:
  - user: any
    ips: [127.0.0.1]
    permissions:
      - action: api
      - action: publish
        path: camera
      - action: read
        path: camera
api: true
apiAddress: 127.0.0.1:%d
metrics: false
pprof: false
playback: false
rtsp: true
rtspAddress: 127.0.0.1:%d
rtspTransports: [tcp]
rtmp: false
hls: false
webrtc: false
srt: false
moq: false
paths:
  camera:
    source: publisher
    record: false
    runOnRecordSegmentComplete: %s recording-complete --spool %s
`, apiPort, rtspPort, hub, m.spool)
	path := filepath.Join(t.TempDir(), "mediamtx.yml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.CreateTemp(t.TempDir(), "mediamtx-log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(bin, path)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := false
	for i := 0; i < 60; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", m.mediaAPI+"/v3/config/paths/get/camera", nil)
		res, e := m.client.Do(req)
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("MediaMTX did not start: %s", data)
	}
	m.state.Recording = Recording{Enabled: true, Bucket: "Temporary", TemporaryDays: 7}
	if err = m.setRecord(ctx, true); err != nil {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("real record configuration: %v; %s", err, data)
	}
	ff := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=10", "-t", "20", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "10", "-bf", "0", "-f", "rtsp", "-rtsp_transport", "tcp", fmt.Sprintf("rtsp://127.0.0.1:%d/camera", rtspPort))
	if data, e := ff.CombinedOutput(); e != nil {
		t.Fatalf("synthetic RTSP publisher: %v %s", e, data)
	}
	if err = m.setRecord(ctx, false); err != nil {
		t.Fatal(err)
	}
	var readyFiles []string
	for i := 0; i < 100; i++ {
		readyFiles, _ = filepath.Glob(filepath.Join(m.spool, "*.mp4.ready"))
		if len(readyFiles) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(readyFiles) < 2 {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("finalized recording hook missing: %s", data)
	}
	for _, marker := range readyFiles {
		clip := strings.TrimSuffix(marker, ".ready")
		out, e := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height", "-of", "json", clip).Output()
		if e != nil {
			t.Fatal("finalized clip was not playable", e)
		}
		var probe struct {
			Streams []struct {
				Codec  string `json:"codec_name"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"streams"`
		}
		if json.Unmarshal(out, &probe) != nil || len(probe.Streams) != 1 || probe.Streams[0].Codec != "h264" || probe.Streams[0].Width != 320 {
			t.Fatal("unexpected recorded codec")
		}
	}
	for i := 0; i < 100; i++ {
		left, _ := filepath.Glob(filepath.Join(m.spool, "*.mp4.ready"))
		if len(left) == 0 {
			break
		}
		if _, err := m.uploadOne(ctx); err != nil {
			t.Fatal(err)
		}
	}
	left, _ := filepath.Glob(filepath.Join(m.spool, "*.mp4"))
	if len(left) != 0 {
		t.Fatal("verified recording uploads did not release local buffer")
	}
	g.mu.Lock()
	recorded := 0
	for _, f := range g.files {
		if f.MimeType == "video/mp4" {
			recorded++
		}
	}
	g.mu.Unlock()
	if recorded < 2 {
		t.Fatal("actual MP4 recordings missing from Drive fixture")
	}
	t.Logf("real MediaMTX v1.21.2 finalized %d playable H.264 MP4 segments; CLI hook created completion markers", len(readyFiles))
}
