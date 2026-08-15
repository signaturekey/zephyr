package agent

import (
	"context"

	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
)

type ContextDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Runtime interface {
	Route(context.Context, routing.Request, *snapshot.Snapshot, []ContextDocument) (protocol.SemanticRoutingEnvelope, error)
	Review(context.Context, string, string, *snapshot.Snapshot, []ContextDocument) (protocol.CandidateEnvelope, error)
	Gate(context.Context, string, []protocol.CandidateFinding, *snapshot.Snapshot, []ContextDocument) (protocol.EvidenceVerdictEnvelope, error)
	Close() error
}
