package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxAPKSize int64 = 150 << 20
const updatePackage = "com.onespace.companion"

type UpdateManifest struct {
	PackageName  string `json:"packageName"`
	VersionCode  int64  `json:"versionCode"`
	VersionName  string `json:"versionName"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	DownloadPath string `json:"downloadPath"`
}

// updateMu guards all APK replacement and serving so clients never see partial files.
var updateMu sync.RWMutex

func (a *App) updateDir() string          { return filepath.Join(a.s.root, "Updates") }
func (a *App) updateManifestFile() string { return filepath.Join(a.updateDir(), "android.json") }
func (a *App) updateApkFile() string      { return filepath.Join(a.updateDir(), "android.apk") }

func (a *App) readUpdate() (*UpdateManifest, error) {
	b, err := os.ReadFile(a.updateManifestFile())
	if err != nil {
		return nil, err
	}
	var u UpdateManifest
	if err = json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	if u.PackageName != updatePackage || u.VersionCode < 1 || u.Size <= 0 || u.Size > maxAPKSize || len(u.SHA256) != 64 || u.DownloadPath != "/v1/update/apk" {
		return nil, errors.New("invalid update manifest")
	}
	return &u, nil
}

func (a *App) uploadUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		bad(w, 405, "Method not allowed")
		return
	}
	if r.ContentLength > maxAPKSize || r.ContentLength == 0 {
		bad(w, 413, "APK must be between 1 byte and 150 MB")
		return
	}
	if err := os.MkdirAll(a.updateDir(), 0700); err != nil {
		bad(w, 500, "Unable to create update folder")
		return
	}
	f, err := os.CreateTemp(a.updateDir(), ".pending-*.apk")
	if err != nil {
		bad(w, 500, "Unable to create temporary APK")
		return
	}
	tempName := f.Name()
	defer os.Remove(tempName)
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(r.Body, maxAPKSize+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		bad(w, 500, "Failed to save APK")
		return
	}
	if size > maxAPKSize {
		bad(w, 413, "APK too large")
		return
	}
	// Derive update metadata from the APK itself; never ask the user to type
	// versionCode/versionName. Android still performs the final signature check.
	info, err := readAPKInfo(tempName)
	if err != nil {
		bad(w, 400, "Could not read APK package/version: "+err.Error())
		return
	}
	update := &UpdateManifest{PackageName: info.Package, VersionCode: info.VersionCode, VersionName: info.VersionName, Size: size,
		SHA256: hex.EncodeToString(hasher.Sum(nil)), DownloadPath: "/v1/update/apk"}
	updateMu.Lock()
	defer updateMu.Unlock()
	if previous, err := a.readUpdate(); err == nil && info.VersionCode <= previous.VersionCode {
		bad(w, 409, fmt.Sprintf("New APK version code must be greater than published version (%d)", previous.VersionCode))
		return
	}
	// Windows does not allow os.Rename to overwrite existing files. Make backups
	// so a failed manifest write can be rolled back without publishing mismatched bytes.
	currentAPK := a.updateApkFile()
	backupAPK := currentAPK + ".old"
	os.Remove(backupAPK)
	if _, err := os.Stat(currentAPK); err == nil {
		if err := os.Rename(currentAPK, backupAPK); err != nil {
			bad(w, 500, "Unable to rotate previous APK")
			return
		}
	}
	if err := os.Rename(tempName, currentAPK); err != nil {
		os.Rename(backupAPK, currentAPK)
		bad(w, 500, "Unable to publish APK")
		return
	}
	manifestBytes, _ := json.MarshalIndent(update, "", "  ")
	manifestTmp := a.updateManifestFile() + ".new"
	if err := os.WriteFile(manifestTmp, manifestBytes, 0600); err != nil {
		os.Remove(currentAPK)
		os.Rename(backupAPK, currentAPK)
		bad(w, 500, "Unable to publish update metadata")
		return
	}
	// Replace manifest after APK while serving is protected by the RW mutex.
	if err := os.Remove(a.updateManifestFile()); err != nil && !os.IsNotExist(err) {
		os.Remove(manifestTmp)
		os.Remove(currentAPK)
		os.Rename(backupAPK, currentAPK)
		bad(w, 500, "Unable to update manifest")
		return
	}
	if err := os.Rename(manifestTmp, a.updateManifestFile()); err != nil {
		os.Remove(currentAPK)
		os.Rename(backupAPK, currentAPK)
		bad(w, 500, "Unable to finalize update manifest")
		return
	}
	os.Remove(backupAPK)
	jsonReply(w, 200, update)
}

func (a *App) getUpdate(w http.ResponseWriter, r *http.Request, local bool) {
	if r.Method != http.MethodGet {
		bad(w, 405, "Method not allowed")
		return
	}
	updateMu.RLock()
	defer updateMu.RUnlock()
	manifest, err := a.readUpdate()
	if err != nil {
		bad(w, 404, "No Android update has been published yet")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/manifest") {
		jsonReply(w, 200, manifest)
		return
	}
	f, err := os.Open(a.updateApkFile())
	if err != nil {
		bad(w, 404, "APK missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != manifest.Size {
		bad(w, 500, "Update file does not match published metadata")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="OneSpace-Android.apk"`)
	w.Header().Set("Cache-Control", "no-store")
	// Limit scope to enrolled devices (enforced by remote handler) or authenticated dashboard.
	_, _ = io.Copy(w, f)
}
