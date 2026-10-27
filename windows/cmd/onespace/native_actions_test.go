package main

import "testing"

func TestNativeActionsPreserveValidationAndPersistence(t *testing.T) {
	root := t.TempDir()
	s, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(s, "pin")
	if code, _ := a.nativeAction("/local/password", map[string]any{"password": "short"}); code != 400 {
		t.Fatalf("short password: %d", code)
	}
	if code, _ := a.nativeAction("/local/password", map[string]any{"password": "a-long-private-password"}); code != 200 {
		t.Fatalf("password: %d", code)
	}
	if code, _ := a.nativeAction("/local/clipboard", map[string]any{"enabled": true}); code != 200 {
		t.Fatalf("clipboard: %d", code)
	}
	if code, _ := a.nativeAction("/local/pair/decision", map[string]any{"id": "missing", "approve": true}); code != 404 {
		t.Fatalf("missing pairing: %d", code)
	}
	if code, _ := a.nativeAction("/local/device/revoke", map[string]any{"id": "any"}); code != 403 {
		t.Fatalf("unlisted action: %d", code)
	}
	again, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Data.AutoClipboard || !again.hasPassword() {
		t.Fatal("native settings not persisted")
	}
}
