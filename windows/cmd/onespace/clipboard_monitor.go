package main

import (
	"bytes"
	"log"
	"onespace.local/desktop/internal/clipboard"
	"time"
)

// Monitor clipboard sequence changes without repeatedly opening or decoding it.
// The owner must explicitly enable this in the Windows dashboard.
func (a *App) startClipboardMonitor(stop <-chan struct{}) {
	// Establish the privacy baseline before listening; never upload an old copy.
	a.checkClipboard()
	changes, err := clipboard.Changes(stop)
	interval := 5 * time.Second
	if err != nil {
		changes = nil
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	retry := time.NewTimer(time.Hour)
	if !retry.Stop() {
		<-retry.C
	}
	defer retry.Stop()
	check := func() {
		a.checkClipboard()
		a.clipMu.Lock()
		busy := a.clipFailures > 0
		a.clipMu.Unlock()
		if busy {
			if !retry.Stop() {
				select {
				case <-retry.C:
				default:
				}
			}
			retry.Reset(200 * time.Millisecond)
		}
	}
	for {
		select {
		case <-stop:
			return
		case _, open := <-changes:
			if !open {
				changes = nil
				ticker.Reset(200 * time.Millisecond)
			} else {
				check()
			}
		case <-ticker.C:
			check()
		case <-retry.C:
			check()
		}
	}
}

func (a *App) checkClipboard() {
	a.clipMu.Lock()
	defer a.clipMu.Unlock()
	seq := clipboard.Sequence()
	if seq == 0 {
		return
	} // non-Windows build
	a.s.Lock()
	enabled := a.s.Data.AutoClipboard
	a.s.Unlock()
	if !a.clipReady {
		a.clipReady = true
		a.clipSeq = seq
		return // never share a pre-existing clipboard on service startup
	}
	if a.clipSeq == seq {
		return
	}
	// Unsupported formats are intentionally bounded by 4 polls.
	if !enabled {
		a.clipSeq = seq
		a.clipFailures = 0
		return
	}
	text, err := clipboard.GetText()
	if err == nil && len(text) > 0 && len(text) <= 250000 {
		a.clipSeq = seq
		a.clipFailures = 0
		it, saveErr := a.saveItem(bytes.NewReader([]byte(text)), "Windows clipboard.txt", "text", "Temporary", "Windows", int64(len(text)))
		if saveErr == nil {
			if !a.hub.queueText(text) {
				a.enqueueToOtherDevices(it, "Windows")
			}
		} else {
			log.Printf("clipboard text queue: %v", saveErr)
		}
		return
	}
	image, err := clipboard.GetImagePNG()
	if err == nil && len(image) > 0 {
		a.clipSeq = seq
		a.clipFailures = 0
		it, saveErr := a.saveItem(bytes.NewReader(image), "Windows clipboard.png", "file", "Temporary", "Windows", int64(len(image)))
		if saveErr == nil {
			a.enqueueToOtherDevices(it, "Windows")
		} else {
			log.Printf("clipboard image queue: %v", saveErr)
		}
		return
	}
	// Other programs can briefly lock the clipboard while changing formats.
	// Retry a few times rather than permanently losing that sequence number.
	// Stop after a bounded number of attempts for unsupported clipboard formats.
	a.clipFailures++
	if a.clipFailures >= 4 {
		a.clipSeq = seq
		a.clipFailures = 0
	}
}
