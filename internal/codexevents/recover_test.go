package codexevents

import (
	"strings"
	"testing"
)

func TestRecoverStructuredOutput(t *testing.T) {
	want := `{"version":1,"run_id":"run-1","role":"architect-reviewer","findings":[]}`
	events := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"item.completed","item":{"id":"warning","type":"error","message":"deprecated option"}}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"answer","type":"agent_message","text":` + quoteJSON(want) + `}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
	}, "\n") + "\n"

	got, err := Recover([]byte(events), KindReviewer)
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if string(got) != want+"\n" {
		t.Fatalf("Recover() = %q, want %q", got, want+"\n")
	}
}

func TestRecoverStructuredOutputKinds(t *testing.T) {
	tests := []struct {
		name    string
		kind    Kind
		message string
	}{
		{name: "routing", kind: KindRouting, message: `{"version":1,"run_id":"run-1","decisions":[]}`},
		{name: "evidence", kind: KindEvidence, message: `{"version":1,"run_id":"run-1","verdicts":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := `{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(test.message) + `}}` + "\n" +
				`{"type":"turn.completed"}` + "\n"
			got, err := Recover([]byte(events), test.kind)
			if err != nil {
				t.Fatalf("Recover() error = %v", err)
			}
			if string(got) != test.message+"\n" {
				t.Fatalf("Recover() = %q, want %q", got, test.message+"\n")
			}
		})
	}
}

func TestRecoverStructuredOutputFailsClosed(t *testing.T) {
	valid := `{"version":1,"run_id":"run-1","role":"architect-reviewer","findings":[]}`
	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{
			name: "missing completed turn",
			events: []string{
				`{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(valid) + `}}`,
			},
			want: "exactly one completed turn",
		},
		{
			name: "multiple messages",
			events: []string{
				`{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(valid) + `}}`,
				`{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(valid) + `}}`,
				`{"type":"turn.completed"}`,
			},
			want: "exactly one agent message",
		},
		{
			name: "error after message",
			events: []string{
				`{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(valid) + `}}`,
				`{"type":"item.completed","item":{"type":"error","message":"terminal failure"}}`,
				`{"type":"turn.completed"}`,
			},
			want: "error event after agent message",
		},
		{
			name: "invalid envelope",
			events: []string{
				`{"type":"item.completed","item":{"type":"agent_message","text":"{}"}}`,
				`{"type":"turn.completed"}`,
			},
			want: "validate recovered reviewer output",
		},
		{
			name: "trailing malformed event",
			events: []string{
				`{"type":"item.completed","item":{"type":"agent_message","text":` + quoteJSON(valid) + `}}`,
				`{"type":"turn.completed"}`,
				`not-json`,
			},
			want: "decode event",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Recover([]byte(strings.Join(test.events, "\n")+"\n"), KindReviewer)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Recover() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func quoteJSON(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, character := range value {
		switch character {
		case '\\', '"':
			builder.WriteByte('\\')
			builder.WriteRune(character)
		case '\n':
			builder.WriteString(`\n`)
		default:
			builder.WriteRune(character)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
