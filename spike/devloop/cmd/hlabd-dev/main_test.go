package main

import "testing"

func TestListenAddrDefault(t *testing.T) {
	if got := listenAddr(); got != "198.18.0.1:8090" {
		t.Fatalf("listenAddr() = %q", got)
	}
}

func TestFailsOnPurpose(t *testing.T) {
	if testingFail := false; testingFail {
		t.Fatal("never")
	}
}
