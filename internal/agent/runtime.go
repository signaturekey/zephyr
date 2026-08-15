package agent

import (
	"context"

	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/snapshot"
)

type ContextDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Runtime interface {
	Route(context.Context, routing.Request, *snapshot.Snapshot, []ContextDocument) (schema.SemanticRoutingEnvelope, error)
	Review(context.Context, string, string, *snapshot.Snapshot, []ContextDocument) (schema.CandidateEnvelope, error)
	Gate(context.Context, string, []schema.CandidateFinding, *snapshot.Snapshot, []ContextDocument) (schema.EvidenceVerdictEnvelope, error)
	Close() error
}
