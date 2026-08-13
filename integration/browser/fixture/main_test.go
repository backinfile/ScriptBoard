package main

import "testing"

func TestFixtureListenAddressDefaultsToEphemeralLoopback(t *testing.T) {
	t.Setenv("SCRIPTBOARD_FIXTURE_LISTEN", "")
	if got := fixtureListenAddress(); got != "127.0.0.1:0" {
		t.Fatalf("default listen address=%q", got)
	}
}

func TestFixtureListenAddressAcceptsExplicitLoopbackPort(t *testing.T) {
	t.Setenv("SCRIPTBOARD_FIXTURE_LISTEN", "127.0.0.1:11149")
	if got := fixtureListenAddress(); got != "127.0.0.1:11149" {
		t.Fatalf("explicit listen address=%q", got)
	}
}

func TestFixtureListenAddressRejectsNonLoopback(t *testing.T) {
	t.Setenv("SCRIPTBOARD_FIXTURE_LISTEN", "0.0.0.0:11149")
	defer func() {
		if recover() == nil {
			t.Fatal("non-loopback fixture address was accepted")
		}
	}()
	_ = fixtureListenAddress()
}
