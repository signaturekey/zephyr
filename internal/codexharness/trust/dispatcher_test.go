package trust

import (
	"os"
	"testing"
	"testing/fstest"
)

func TestVerifyDispatcherRequiresEmbeddedDigest(t *testing.T) {
	path := t.TempDir() + "/dispatch.sh"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDispatcher(path, fstest.MapFS{}); err == nil {
		t.Fatal("expected manifest failure")
	}
}
