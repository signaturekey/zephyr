package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const Version = 1

type Category string

const (
	CategoryAuth                Category = "auth"
	CategoryConfig              Category = "config"
	CategorySandbox             Category = "sandbox"
	CategoryRateLimit           Category = "rate-limit"
	CategoryProviderUnavailable Category = "provider-unavailable"
	CategoryTransport           Category = "transport"
	CategoryTimeout             Category = "timeout"
	CategoryValidation          Category = "validation"
	CategoryLifecycle           Category = "lifecycle"
	CategoryUnknown             Category = "unknown"
)

type TerminalState string

const (
	TerminalComplete   TerminalState = "complete"
	TerminalIncomplete TerminalState = "incomplete"
	TerminalFailed     TerminalState = "failed"
)

type Warning string

const WarningPrivateRetained Warning = "private diagnostics retained; handle as sensitive data"

type Event struct {
	Stage                string   `json:"stage"`
	Role                 string   `json:"role,omitempty"`
	Category             Category `json:"category"`
	ReasonCode           string   `json:"reason_code"`
	ExitCode             int      `json:"exit_code"`
	TimedOut             bool     `json:"timed_out"`
	Attempt              int      `json:"attempt"`
	EffectiveConcurrency int      `json:"effective_concurrency"`
	StderrBytes          int      `json:"stderr_bytes"`
	StderrSHA256         string   `json:"stderr_sha256"`
}

type CoverageCounts struct {
	Selected  int `json:"selected"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

type Document struct {
	Version          int            `json:"version"`
	OperationID      string         `json:"operation_id"`
	RunID            string         `json:"run_id,omitempty"`
	CoreVersion      string         `json:"core_version,omitempty"`
	CodexVersion     string         `json:"codex_version,omitempty"`
	CoreSHA256       string         `json:"core_sha256,omitempty"`
	CodexSHA256      string         `json:"codex_sha256,omitempty"`
	PolicySHA256     string         `json:"policy_sha256,omitempty"`
	DispatcherSHA256 string         `json:"dispatcher_sha256,omitempty"`
	TerminalState    TerminalState  `json:"terminal_state"`
	Coverage         CoverageCounts `json:"coverage"`
	Events           []Event        `json:"events"`
	Warnings         []Warning      `json:"warnings,omitempty"`
}

var (
	operationIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	runIDPattern       = regexp.MustCompile(`^(?:run-[0-9a-f]{4,64}|[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{12})$`)
	versionPattern     = regexp.MustCompile(`^(?:v|codex-)?[0-9]+(?:\.[0-9]+){1,3}(?:[-+][0-9A-Za-z.]+)?$`)
	hexSHA256          = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func NewEvent(stage, role string, category Category, reasonCode string, exitCode int, timedOut bool, attempt, effectiveConcurrency int, stderr []byte) Event {
	digest := sha256.Sum256(stderr)
	return Event{
		Stage:                stage,
		Role:                 role,
		Category:             category,
		ReasonCode:           reasonCode,
		ExitCode:             exitCode,
		TimedOut:             timedOut,
		Attempt:              attempt,
		EffectiveConcurrency: effectiveConcurrency,
		StderrBytes:          len(stderr),
		StderrSHA256:         hex.EncodeToString(digest[:]),
	}
}

func Marshal(document Document) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode diagnostics: %w", err)
	}
	return append(data, '\n'), nil
}

func (document Document) Validate() error {
	if document.Version != Version {
		return fmt.Errorf("unsupported diagnostics version %d", document.Version)
	}
	if !operationIDPattern.MatchString(document.OperationID) {
		return errors.New("diagnostics operation_id is invalid")
	}
	if document.RunID != "" && !runIDPattern.MatchString(document.RunID) {
		return errors.New("diagnostics run_id is invalid")
	}
	if document.CoreVersion != "" && !versionPattern.MatchString(document.CoreVersion) {
		return errors.New("diagnostics core_version is invalid")
	}
	if document.CodexVersion != "" && !versionPattern.MatchString(document.CodexVersion) {
		return errors.New("diagnostics codex_version is invalid")
	}
	for name, value := range map[string]string{
		"core_sha256": document.CoreSHA256, "codex_sha256": document.CodexSHA256,
		"policy_sha256": document.PolicySHA256, "dispatcher_sha256": document.DispatcherSHA256,
	} {
		if value != "" && !hexSHA256.MatchString(value) {
			return fmt.Errorf("diagnostics %s is not a SHA-256 digest", name)
		}
	}
	if document.TerminalState != TerminalComplete && document.TerminalState != TerminalIncomplete && document.TerminalState != TerminalFailed {
		return fmt.Errorf("unknown terminal state %q", document.TerminalState)
	}
	if document.Coverage.Selected < 0 || document.Coverage.Completed < 0 || document.Coverage.Failed < 0 || document.Coverage.Completed+document.Coverage.Failed > document.Coverage.Selected {
		return errors.New("invalid diagnostics coverage counts")
	}
	for index, event := range document.Events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("event %d: %w", index, err)
		}
	}
	for _, warning := range document.Warnings {
		if warning != WarningPrivateRetained {
			return fmt.Errorf("unknown diagnostics warning %q", warning)
		}
	}
	return nil
}

func (event Event) Validate() error {
	if !validStage(event.Stage) {
		return errors.New("event stage is invalid")
	}
	if !validRole(event.Role) {
		return errors.New("event role is invalid")
	}
	if !validReasonCode(event.ReasonCode) {
		return errors.New("event reason_code is invalid")
	}
	if !validCategory(event.Category) {
		return fmt.Errorf("unknown category %q", event.Category)
	}
	if event.Attempt < 0 || event.EffectiveConcurrency < 0 || event.StderrBytes < 0 {
		return errors.New("event counts must not be negative")
	}
	if !hexSHA256.MatchString(strings.ToLower(event.StderrSHA256)) {
		return errors.New("event stderr_sha256 is invalid")
	}
	return nil
}

func validStage(stage string) bool {
	switch stage {
	case "preflight", "probe", "routing", "review", "evidence", "aggregate", "render", "lifecycle", "dispatch":
		return true
	default:
		return false
	}
}

func validRole(role string) bool {
	switch role {
	case "", "semantic-router", "code-reviewer", "architect-reviewer", "golang-expert", "python-expert",
		"typescript-expert", "react-expert", "frontend-expert", "skill-authoring-expert", "reliability-expert",
		"messaging-expert", "infrastructure-expert", "storage-expert", "security-auditor", "sql-expert",
		"contract-reviewer", "qa-expert", "code-simplifier", "evidence-gate":
		return true
	default:
		return false
	}
}

func validReasonCode(reason string) bool {
	switch reason {
	case "codex-auth-failed", "configuration-invalid", "sandbox-denied", "rate-limit-exceeded",
		"provider-unavailable", "connection-reset", "deadline", "output-limit", "process-start-failed",
		"process-exit", "validation-failed", "lifecycle-failed":
		return true
	default:
		return false
	}
}

func validCategory(category Category) bool {
	switch category {
	case CategoryAuth, CategoryConfig, CategorySandbox, CategoryRateLimit, CategoryProviderUnavailable,
		CategoryTransport, CategoryTimeout, CategoryValidation, CategoryLifecycle, CategoryUnknown:
		return true
	default:
		return false
	}
}
