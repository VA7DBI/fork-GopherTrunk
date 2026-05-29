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
	if dto.LastFatalClass != "bind_conflict" {
		t.Fatalf("LastFatalClass=%q want bind_conflict", dto.LastFatalClass)
	}
	if dto.LastFatalHint == "" {
		t.Fatal("LastFatalHint is empty")
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

func TestRuntimeSnapshotClassifiesInstanceLock(t *testing.T) {
	d := &Daemon{}
	d.recordFatalWithSource("", errors.New("another gophertrunk is running against .\\2m.yml (.gophertrunk.lock exists)"))

	rs := runtimeSnapshot{
		cfg:    &config.Config{},
		daemon: d,
	}

	dto := rs.Runtime()
	if dto.LastFatalClass != "instance_lock" {
		t.Fatalf("LastFatalClass=%q want instance_lock", dto.LastFatalClass)
	}
	if dto.LastFatalHint == "" {
		t.Fatal("LastFatalHint is empty")
	}
}
