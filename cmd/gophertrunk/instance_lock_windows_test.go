//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstanceLockWindows_AcquireAndRelease(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("log:\n  level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	release, err := acquireInstanceLock(cfgPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	_, err2 := acquireInstanceLock(cfgPath)
	if err2 == nil {
		t.Fatal("expected second acquire to fail")
	}
	msg := err2.Error()
	if !strings.Contains(msg, "another gophertrunk is running") {
		t.Fatalf("unexpected lock contention error: %q", msg)
	}
	if !strings.Contains(msg, "pid=") || !strings.Contains(msg, "started=") {
		t.Fatalf("expected lock metadata in error: %q", msg)
	}
	if !strings.Contains(msg, "owner check: Get-Process -Id") {
		t.Fatalf("expected owner check hint in error: %q", msg)
	}

	release()
	release2, err := acquireInstanceLock(cfgPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}
