package db

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestWrapConnectError_NilPassthrough(t *testing.T) {
	if got := WrapConnectError(nil, "localhost", 5433); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}

func TestWrapConnectError_NonConnectErrPassesThrough(t *testing.T) {
	plain := errors.New("relation \"foo\" does not exist")
	got := WrapConnectError(plain, "localhost", 5433)
	if got != plain {
		t.Fatalf("expected non-connect err to pass through unchanged, got %v", got)
	}
}

func TestWrapConnectError_NetOpErrFormatted(t *testing.T) {
	netErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("connect: connection refused"),
	}
	got := WrapConnectError(netErr, "127.0.0.1", 5433)
	if got == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	msg := got.Error()
	for _, want := range []string{
		"Cannot connect to platform database at 127.0.0.1:5433.",
		"Run `lw dev start`",
		"brew services start postgresql@14",
		"set LW_DB_URL",
		"Original error:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull message: %s", want, msg)
		}
	}
}

func TestWrapConnectError_DNSErrFormatted(t *testing.T) {
	dnsErr := &net.DNSError{Name: "nowhere.invalid", Err: "no such host"}
	got := WrapConnectError(dnsErr, "nowhere.invalid", 5432)
	if got == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	if !strings.Contains(got.Error(), "Cannot connect to platform database at nowhere.invalid:5432.") {
		t.Errorf("expected host:port in message, got: %s", got.Error())
	}
}

func TestWrapConnectError_PreservesUnwrap(t *testing.T) {
	netErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	got := WrapConnectError(netErr, "localhost", 5433)
	// Wrapped via %w → errors.Is should reach the underlying net.OpError.
	if !errors.Is(got, netErr) {
		t.Error("expected errors.Is to find wrapped net.OpError via %w chain")
	}
}
