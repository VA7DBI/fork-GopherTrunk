package main

import (
	"errors"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/config"
)

func TestRuntimeSnapshotIncludesFatalStatus(t *testing.T) {
	d := &Daemon{}
	d.recordFatalWithSource("http", errors.New("http: bind: address already in use"))

	rs := runtimeSnapshot{
		cfg:     &config.Config{},
		version: "test-version",
		daemon:  d,
	}

	dto := rs.Runtime()
	if dto.LastFatalError == "" {
		t.Fatal("LastFatalError is empty")
	}
	if dto.LastFatalComponent != "http" {
		t.Fatalf("LastFatalComponent=%q want %q", dto.LastFatalComponent, "http")
	}
	if dto.LastFatalAt.IsZero() {
		t.Fatal("LastFatalAt is zero")
	}
}

func TestRuntimeSnapshotFatalStatusFirstWins(t *testing.T) {
	d := &Daemon{}
	d.recordFatalWithSource("http", errors.New("http failed"))
	d.recordFatalWithSource("grpc", errors.New("grpc failed"))

	rs := runtimeSnapshot{
		cfg:    &config.Config{},
		daemon: d,
	}

	dto := rs.Runtime()
	if dto.LastFatalComponent != "http" {
		t.Fatalf("LastFatalComponent=%q want first source http", dto.LastFatalComponent)
	}
}
