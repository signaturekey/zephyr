package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	schemaassets "github.com/signaturekey/zephyr/schemas"
)

const (
	candidateFindingsSchema = "candidate-findings.codex.schema.json"
	evidenceVerdictSchema   = "evidence-verdict.codex.schema.json"
	semanticRoutingSchema   = "semantic-routing.codex.schema.json"
)

var ErrInvalidDocument = errors.New("invalid zephyr protocol document")

var compiledSchemas sync.Map

func ValidateCandidateBytes(data []byte) (CandidateEnvelope, error) {
	if err := validateDocument(candidateFindingsSchema, data); err != nil {
		return CandidateEnvelope{}, err
	}

	var envelope CandidateEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return CandidateEnvelope{}, fmt.Errorf("%w: decode candidate findings: %v", ErrInvalidDocument, err)
	}
	if err := validateCandidateSemantics(envelope); err != nil {
		return CandidateEnvelope{}, err
	}
	return envelope, nil
}

func ValidateVerdictBytes(data []byte) (EvidenceVerdictEnvelope, error) {
	if err := validateDocument(evidenceVerdictSchema, data); err != nil {
		return EvidenceVerdictEnvelope{}, err
	}

	var envelope EvidenceVerdictEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return EvidenceVerdictEnvelope{}, fmt.Errorf("%w: decode evidence verdicts: %v", ErrInvalidDocument, err)
	}
	if err := validateVerdictSemantics(envelope); err != nil {
		return EvidenceVerdictEnvelope{}, err
	}
	return envelope, nil
}

func ValidateSemanticRoutingBytes(data []byte) (SemanticRoutingEnvelope, error) {
	if err := validateDocument(semanticRoutingSchema, data); err != nil {
		return SemanticRoutingEnvelope{}, err
	}
	var envelope SemanticRoutingEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return SemanticRoutingEnvelope{}, fmt.Errorf("%w: decode semantic routing: %v", ErrInvalidDocument, err)
	}
	seen := make(map[string]struct{}, len(envelope.Decisions))
	for _, decision := range envelope.Decisions {
		if _, duplicate := seen[decision.Role]; duplicate {
			return SemanticRoutingEnvelope{}, fmt.Errorf("%w: duplicate semantic routing decision for role %q", ErrInvalidDocument, decision.Role)
		}
		seen[decision.Role] = struct{}{}
	}
	return envelope, nil
}

func ValidateVerdictCandidateIDs(envelope EvidenceVerdictEnvelope, candidateIDs []string) error {
	expected := make(map[string]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		if id == "" {
			return fmt.Errorf("%w: candidate ID set contains an empty ID", ErrInvalidDocument)
		}
		if _, duplicate := expected[id]; duplicate {
			return fmt.Errorf("%w: candidate ID set contains duplicate %q", ErrInvalidDocument, id)
		}
		expected[id] = struct{}{}
	}

	seen := make(map[string]struct{}, len(envelope.Verdicts))
	for _, verdict := range envelope.Verdicts {
		if _, ok := expected[verdict.CandidateID]; !ok {
			return fmt.Errorf("%w: verdict references unknown candidate %q", ErrInvalidDocument, verdict.CandidateID)
		}
		seen[verdict.CandidateID] = struct{}{}
		if verdict.DuplicateOf != nil {
			if _, ok := expected[*verdict.DuplicateOf]; !ok {
				return fmt.Errorf("%w: duplicate verdict for %q references unknown canonical candidate %q", ErrInvalidDocument, verdict.CandidateID, *verdict.DuplicateOf)
			}
		}
	}
	for _, id := range candidateIDs {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("%w: evidence gate omitted candidate %q", ErrInvalidDocument, id)
		}
	}
	return nil
}

func validateDocument(name string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("%w: %s input is empty", ErrInvalidDocument, name)
	}

	compiled, err := getCompiledSchema(name)
	if err != nil {
		return err
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: decode %s input: %v", ErrInvalidDocument, name, err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("%w: validate %s: %v", ErrInvalidDocument, name, err)
	}
	return nil
}

func getCompiledSchema(name string) (*jsonschema.Schema, error) {
	if cached, ok := compiledSchemas.Load(name); ok {
		return cached.(*jsonschema.Schema), nil
	}

	raw, err := schemaassets.Read(name)
	if err != nil {
		return nil, fmt.Errorf("load embedded schema %q: %w", name, err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode embedded schema %q: %w", name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	if err := compiler.AddResource(name, document); err != nil {
		return nil, fmt.Errorf("register embedded schema %q: %w", name, err)
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("compile embedded schema %q: %w", name, err)
	}

	actual, _ := compiledSchemas.LoadOrStore(name, compiled)
	return actual.(*jsonschema.Schema), nil
}

func validateCandidateSemantics(envelope CandidateEnvelope) error {
	seen := make(map[string]struct{}, len(envelope.Findings))
	for i, finding := range envelope.Findings {
		if finding.Role != envelope.Role {
			return fmt.Errorf("%w: findings[%d] role %q differs from envelope role %q", ErrInvalidDocument, i, finding.Role, envelope.Role)
		}
		if _, duplicate := seen[finding.ID]; duplicate {
			return fmt.Errorf("%w: duplicate finding ID %q", ErrInvalidDocument, finding.ID)
		}
		seen[finding.ID] = struct{}{}
		if finding.Location.LineEnd != 0 && finding.Location.LineStart != 0 && finding.Location.LineEnd < finding.Location.LineStart {
			return fmt.Errorf("%w: finding %q has line_end before line_start", ErrInvalidDocument, finding.ID)
		}
	}
	return nil
}

func validateVerdictSemantics(envelope EvidenceVerdictEnvelope) error {
	seen := make(map[string]struct{}, len(envelope.Verdicts))
	for _, verdict := range envelope.Verdicts {
		if _, duplicate := seen[verdict.CandidateID]; duplicate {
			return fmt.Errorf("%w: duplicate verdict for candidate %q", ErrInvalidDocument, verdict.CandidateID)
		}
		seen[verdict.CandidateID] = struct{}{}
		if verdict.DuplicateOf != nil && *verdict.DuplicateOf == verdict.CandidateID {
			return fmt.Errorf("%w: candidate %q cannot be a duplicate of itself", ErrInvalidDocument, verdict.CandidateID)
		}
		switch verdict.Verdict {
		case VerdictAccepted, VerdictDowngraded:
			if verdict.FinalSeverity == nil || verdict.DuplicateOf != nil {
				return fmt.Errorf("%w: %s candidate %q requires final_severity and no duplicate_of", ErrInvalidDocument, verdict.Verdict, verdict.CandidateID)
			}
		case VerdictRejected:
			if verdict.FinalSeverity != nil || verdict.DuplicateOf != nil {
				return fmt.Errorf("%w: rejected candidate %q cannot set final_severity or duplicate_of", ErrInvalidDocument, verdict.CandidateID)
			}
		case VerdictDuplicate:
			if verdict.FinalSeverity != nil || verdict.DuplicateOf == nil {
				return fmt.Errorf("%w: duplicate candidate %q requires duplicate_of and null final_severity", ErrInvalidDocument, verdict.CandidateID)
			}
		case VerdictNeedsHuman:
			if verdict.DuplicateOf != nil {
				return fmt.Errorf("%w: needs-human candidate %q cannot set duplicate_of", ErrInvalidDocument, verdict.CandidateID)
			}
		}
	}
	return nil
}
