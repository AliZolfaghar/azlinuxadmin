package network

import "testing"

func TestDetectManager(t *testing.T) {
	d := DetectManager()
	if d.Label == "" {
		t.Fatal("expected non-empty Label")
	}
	if d.Manager == "" {
		t.Fatal("expected non-empty Manager")
	}
	t.Logf("detected manager=%s label=%q writable=%v — %s", d.Manager, d.Label, d.Writable, d.Reason)

	s := LoadSnapshot()
	if s.Detection.Manager != d.Manager {
		t.Fatalf("snapshot manager %q != detect %q", s.Detection.Manager, d.Manager)
	}
	if s.Hostname == "" {
		t.Fatal("expected hostname")
	}
}
