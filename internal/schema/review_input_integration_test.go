package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "build packet")
	data, err := json.Marshal(result.Packet)
	require.NoError(t, err, "marshal packet")
	if err := schema.ValidateReviewInputBytes(data); err != nil {
		t.Fatalf("generated packet violates review-input schema: %v\npacket: %s", err, data)
	}
}
