// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// A companion is opened exactly when the main listener does not already accept
// on loopback - and never twice on the same address, which would be a bind
// conflict on every default install.
func TestLoopbackAddr(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"", ""},              // ":6888" - wildcard, covers loopback
		{"0.0.0.0", ""},       // Go opens this as a dual-stack [::] listen
		{"::", ""},            // and this is that listen, named
		{"127.0.0.1", ""},     // already there
		{"127.0.1.1", ""},     // the whole 127/8 is loopback
		{"::1", ""},           // already there
		{"localhost", ""},     // a name: not ours to resolve, and it IS loopback
		{"wudict.lan", ""},    // any other name, likewise left alone
		{"fe80::1%wlan0", ""}, // a zone: ParseIP cannot read it
		{"192.168.1.5", "127.0.0.1:6888"},
		{"10.0.0.1", "127.0.0.1:6888"},
		{"::ffff:192.168.1.5", "127.0.0.1:6888"}, // v4-mapped is a v4 address
		{"2001:db8::1", "[::1]:6888"},
	} {
		if got := loopbackAddr(tc.ip, "6888"); got != tc.want {
			t.Errorf("loopbackAddr(%q) = %q, want %q", tc.ip, got, tc.want)
		}
	}
}

// The point of the whole change: one Server over two listeners, where losing
// the second is a log line and losing the first is the end of the program.
// Closing a listener out from under Serve is what the kernel does when the
// address it is bound to leaves the interface.
func TestServeSurvivesLosingASecondListener(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(second) }()
	go func() { done <- srv.Serve(first) }()

	// The address goes away.
	second.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("second listener ended with %v, want a closed-listener error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its listener was closed")
	}

	// The first one is still serving, which is the whole point.
	resp, err := http.Get("http://" + first.Addr().String() + "/")
	if err != nil {
		t.Fatalf("the surviving listener stopped answering: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// And one Shutdown ends both, including the listener already gone.
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("the first listener ended with %v, want ErrServerClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}
