package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeWorkspaceAndFileWorkflow(t *testing.T) {
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(state, "public-pin")
	pages := a.nativePages()
	page := func(name string) nativePage {
		for _, p := range pages {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("missing page %s", name)
		return nativePage{}
	}
	invoke := func(p nativePage, name string, values []string) string {
		t.Helper()
		for _, b := range p.Buttons {
			if b.Name == name {
				code, body := b.Run(context.Background(), values)
				if code >= 300 {
					t.Fatalf("%s: %d %s", name, code, body)
				}
				return body
			}
		}
		t.Fatalf("missing action %s", name)
		return ""
	}
	invoke(page("Tasks"), "Save task", []string{"", "Test shared task", "false"})
	invoke(page("Notes"), "Save note", []string{"", "Test title", "Private content"})
	filename := filepath.Join(t.TempDir(), "sample.txt")
	if err = os.WriteFile(filename, []byte("sample bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	body := invoke(page("Files & clipboard"), "Import file", []string{filename, "", "Temporary", "", ""})
	var item Item
	if err = json.Unmarshal([]byte(body), &item); err != nil {
		t.Fatal(err)
	}
	invoke(page("Files & clipboard"), "Keep permanently", []string{"", "", "", item.ID, ""})
	reloaded, err := NewState(state.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Data.Tasks) != 1 || len(reloaded.Data.Notes) != 1 || len(reloaded.Data.Items) != 1 || reloaded.Data.Items[0].Bucket != "Permanent" {
		t.Fatal("native actions did not persist")
	}
	invoke(page("Files & clipboard"), "Delete item", []string{"", "", "", item.ID, ""})
	if _, err = os.Stat(filepath.Join(state.root, "Permanent", filepath.Base(item.RelPath))); !os.IsNotExist(err) {
		t.Fatal("native delete left item")
	}
	for _, p := range pages {
		for _, b := range p.Buttons {
			if strings.Contains(b.Name, "Delete") || strings.Contains(b.Name, "Revoke") {
				if !b.Destructive {
					t.Fatalf("destructive action lacks confirmation: %s", b.Name)
				}
			}
		}
	}
}
func TestNativeOutputDoesNotExposeCredentials(t *testing.T) {
	formatted := nativeReadable(`{"name":"phone","token":"private-device-token","protectedToken":"private-dpapi-data","ticket":"private-ticket","fingerprint":"public-pin","nextPageToken":"public-page-cursor"}`)
	if strings.Contains(formatted, "private-") || !strings.Contains(formatted, "public-pin") || !strings.Contains(formatted, "public-page-cursor") {
		t.Fatal("native display leaked credentials or hid public identity")
	}
	for _, bad := range []string{"../file", "id?secret", "https://host", ""} {
		if validNativeItem(bad) || validNativeCloudID(bad) {
			t.Fatal("accepted injected identifier")
		}
	}
}

func TestCancelledNativeImportDoesNotPersistPartialFile(t *testing.T) {
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(state, "pin")
	file := filepath.Join(t.TempDir(), "source.bin")
	if err = os.WriteFile(file, []byte("source remains private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _ := a.nativeUpload(ctx, file, "/local/v1/items/file?name=source.bin&bucket=Temporary")
	if code < 300 || len(state.Data.Items) != 0 {
		t.Fatal("cancelled import committed")
	}
	entries, err := os.ReadDir(filepath.Join(state.root, "Temporary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("partial staged file retained")
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("source was removed")
	}
}
