package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var recordingName = regexp.MustCompile(`^([tp])([0-9]{1,3})_[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{6}\.mp4$`)

// CompleteSegment is invoked locally by MediaMTX only after closing an fMP4
// segment. The filename carries the policy selected when its recording began.
// No credentials, hub state lock, network call or footage content is needed.
func CompleteSegment(spool, segment string) error {
	base, err := filepath.Abs(spool)
	if err != nil {
		return err
	}
	path, err := filepath.Abs(segment)
	if err != nil {
		return err
	}
	if filepath.Dir(path) != base {
		return errors.New("completed segment is outside the configured spool")
	}
	match := recordingName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return errors.New("unrecognized recording filename")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("completed recording must be a non-empty regular file")
	}
	days, _ := strconv.Atoi(match[2])
	bucket := "Temporary"
	if match[1] == "p" {
		bucket = "Permanent"
	}
	policy := Recording{Bucket: bucket, TemporaryDays: days}
	if !validRecording(policy) {
		return errors.New("invalid segment retention")
	}
	return atomicJSON(path+".ready", policy)
}
func (m *Manager) spoolUsage() (int64, int, error) {
	entries, err := os.ReadDir(m.spool)
	if err != nil {
		return 0, 0, err
	}
	var bytes int64
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".mp4") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, 0, errors.New("non-regular recording in spool")
		}
		bytes += info.Size()
		count++
	}
	return bytes, count, nil
}
func (m *Manager) setRecord(ctx context.Context, enabled bool) error {
	patch := map[string]any{"record": enabled}
	if enabled {
		m.mu.Lock()
		policy := m.state.Recording
		m.mu.Unlock()
		mode := "t"
		if policy.Bucket == "Permanent" {
			mode = "p"
		}
		patch["recordPath"] = filepath.Join(filepath.Dir(m.spool), "%path", fmt.Sprintf("%s%d_%%Y-%%m-%%d_%%H-%%M-%%S-%%f", mode, policy.TemporaryDays))
		patch["recordFormat"] = "fmp4"
		patch["recordPartDuration"] = "1s"
		patch["recordMaxPartSize"] = "8M"
		patch["recordSegmentDuration"] = "15s"
		patch["recordDeleteAfter"] = "0s"
	}
	b, _ := json.Marshal(patch)
	req, _ := http.NewRequestWithContext(ctx, "PATCH", m.mediaAPI+"/v3/config/paths/patch/camera", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := m.client.Do(req)
	if err != nil {
		return errors.New("recording controller unavailable; check the loopback MediaMTX API and recording configuration")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode != 200 {
		return fmt.Errorf("MediaMTX rejected recording configuration (HTTP %d)", res.StatusCode)
	}
	return nil
}
func (m *Manager) recordLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	initialized := false
	retry := time.Time{}
	checkAfter := time.Time{}
	controlError := ""
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		m.recordOp.Lock()
		m.mu.Lock()
		enabled := m.state.Recording.Enabled
		active := m.active
		configured := m.state.ControllerConfigured
		m.mu.Unlock()
		bytes, count, err := m.spoolUsage()
		reason := ""
		if err != nil {
			reason = "Recording spool cannot be inspected"
		} else if bytes >= 96<<20 || count >= 96 {
			reason = "Recording paused: local upload buffer is full; completed clips are retained"
		}
		free, err := m.freeSpace(m.dir)
		if err != nil || free < 512<<20 {
			reason = "Recording paused: keep at least 512 MiB free on the Pi SD card"
		}
		spoolFree, spoolErr := m.freeSpace(m.spool)
		if spoolErr != nil || spoolFree < 8<<20 {
			reason = "Recording paused: less than 8 MiB free in the bounded recording buffer"
		}
		if !m.bufferReady(m.spool, m.dir) {
			reason = "Recording paused: bounded recording filesystem is not mounted; run setup-recording-buffer.sh"
		}
		if !m.drive.Connected() {
			reason = "Recording paused: connect Google Drive first"
		}
		wanted := enabled && reason == ""
		// MediaMTX can restart independently and reload record:false from disk.
		// Reconcile its real configuration instead of trusting an old local flag.
		if configured && active && time.Now().After(checkAfter) {
			req, _ := http.NewRequestWithContext(m.ctx, "GET", m.mediaAPI+"/v3/config/paths/get/camera", nil)
			res, e := m.client.Do(req)
			if e != nil {
				active = false
				controlError = "Recording controller unreachable; actual recording state is unknown"
			}
			if e == nil {
				var remote struct {
					Record bool `json:"record"`
				}
				if res.StatusCode == 200 && json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&remote) == nil {
					active = remote.Record
				}
				res.Body.Close()
			}
			checkAfter = time.Now().Add(10 * time.Second)
		}
		// Retry failed local control without flooding logs/API every 2 seconds.
		if configured && (wanted != active || !initialized) && time.Now().After(retry) {
			err = m.setRecord(m.ctx, wanted)
			if err != nil {
				controlError = err.Error()
				retry = time.Now().Add(10 * time.Second)
			} else {
				controlError = ""
				active = wanted
				initialized = true
				retry = time.Time{}
			}
		}
		m.mu.Lock()
		m.active = active
		if controlError != "" {
			if reason != "" {
				reason += "; "
			}
			reason += controlError
		}
		if enabled || active || !initialized {
			if reason != "" || initialized {
				m.recordError = reason
			}
		} else {
			m.recordError = ""
		}
		m.mu.Unlock()
		m.recordOp.Unlock()
	}
}
