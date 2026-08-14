package dispatch

import "testing"

func TestClientRejectsRelativeOutput(t *testing.T) {
	client := New("/bin/true", nil)
	if _, err := client.Probe(t.Context(), ProbeRequest{OutputPath: "relative.json"}); err == nil {
		t.Fatal("Probe accepted a relative output path")
	}
}
