package output

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/signaturekey/zephyr/internal/schema"
)

type Kind string

const (
	KindReviewer Kind = "reviewer"
	KindRouting  Kind = "routing"
	KindEvidence Kind = "evidence"
)

type event struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

func Recover(data []byte, kind Kind) ([]byte, error) {
	if kind != KindReviewer && kind != KindRouting && kind != KindEvidence {
		return nil, fmt.Errorf("unsupported Codex output kind %q", kind)
	}

	reader := bufio.NewReader(bytes.NewReader(data))
	completedTurns := 0
	agentMessages := 0
	agentMessageIndex := -1
	eventIndex := 0
	message := ""
	errorAfterMessage := false
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) != 0 {
			var current event
			if decodeErr := json.Unmarshal(line, &current); decodeErr != nil {
				return nil, fmt.Errorf("decode event %d: %w", eventIndex, decodeErr)
			}
			if current.Type == "turn.completed" {
				completedTurns++
			}
			if current.Type == "item.completed" && current.Item.Type == "agent_message" {
				agentMessages++
				agentMessageIndex = eventIndex
				message = current.Item.Text
			}
			if agentMessageIndex >= 0 && eventIndex > agentMessageIndex && current.Type == "item.completed" && current.Item.Type == "error" {
				errorAfterMessage = true
			}
			eventIndex++
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read Codex events: %w", err)
		}
	}

	if completedTurns != 1 {
		return nil, fmt.Errorf("expected exactly one completed turn, got %d", completedTurns)
	}
	if agentMessages != 1 {
		return nil, fmt.Errorf("expected exactly one agent message, got %d", agentMessages)
	}
	if errorAfterMessage {
		return nil, errors.New("error event after agent message")
	}
	output := []byte(message)
	if err := validate(output, kind); err != nil {
		return nil, err
	}
	return output, nil
}

func WriteRecovered(ctx context.Context, path string, output []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".zephyr-recovered-*")
	if err != nil {
		return fmt.Errorf("create recovery file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set recovery file mode: %w", err)
	}
	if _, err := temporary.Write(output); err != nil {
		return fmt.Errorf("write recovered output: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync recovered output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close recovered output: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish recovered output %q: %w", path, err)
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open recovery directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync recovery directory: %w", err)
	}
	return nil
}

func validate(output []byte, kind Kind) error {
	switch kind {
	case KindReviewer:
		if _, err := schema.ValidateCandidateBytes(output); err != nil {
			return fmt.Errorf("validate recovered reviewer output: %w", err)
		}
	case KindRouting:
		if _, err := schema.ValidateSemanticRoutingBytes(output); err != nil {
			return fmt.Errorf("validate recovered routing output: %w", err)
		}
	case KindEvidence:
		if _, err := schema.ValidateVerdictBytes(output); err != nil {
			return fmt.Errorf("validate recovered evidence output: %w", err)
		}
	}
	return nil
}
