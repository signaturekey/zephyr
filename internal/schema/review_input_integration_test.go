package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
)

func TestContextPacketConformsToReviewInputSchema(t *testing.T) {
	result, err := contextpack.Build(contextpack.Options{
		RunDir: t.TempDir(),
		RunID:  "run-schema-integration",
		Mode:   "plan",
		Source: "plan-only",
		Repository: contextpack.Repository{
			Root: "",
		},
	})
	if err != nil {
		t.Fatalf("Build packet: %v", err)
	}
	data, err := json.Marshal(result.Packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	if err := schema.ValidateReviewInputBytes(data); err != nil {
		t.Fatalf("generated packet violates review-input schema: %v\npacket: %s", err, data)
	}
}
