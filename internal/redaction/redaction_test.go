package redaction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathDenied(t *testing.T) {
	p := DefaultPolicy([]string{"private/**"})

	tests := map[string]bool{
		".env":                  true,
		"config/.env.local":     true,
		"keys/server.pem":       true,
		"keys/client.p12":       true,
		"keys/client.pfx":       true,
		".netrc":                true,
		"CLIENT.P12":            true,
		"keys/SECRET.PEM":       true,
		"HOME/.NETRC":           true,
		"CONFIG/.ENV":           true,
		".envfoo":               true,
		"private/notes.txt":     true,
		"PRIVATE/notes.txt":     false,
		"internal/service.go":   false,
		"fixtures/secret.go":    true,
		"docs/credentials.json": true,
	}
	for path, want := range tests {
		assert.Equal(t, want, p.PathDenied(path), "path denied (%q)", path)
	}
}

func TestInvalidPatternFailsClosed(t *testing.T) {
	p := Policy{Enabled: true, DenyPatterns: []string{"["}}
	require.True(t, p.PathDenied("safe.go"), "invalid deny pattern must fail closed")
}

func TestTextRedactsCommonSecrets(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abc.def.ghi",
		"password=hunter2",
		"https://alice:supersecret@example.test/path",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
	}, "\n")

	got := DefaultPolicy(nil).Text(input)
	for secretID, secret := range []string{"abc.def.ghi", "hunter2", "supersecret", "\nsecret\n"} {
		if strings.Contains(got, secret) {
			t.Errorf("sanitised output contains secret %d", secretID)
		}
	}
}

func TestTextRedactsQuotedJSONAndQueryTokens(t *testing.T) {
	policy := DefaultPolicy(nil)
	input := `{"token":"abc123","client_secret":"xyz"} https://example.invalid/callback?access_token=live-value&next=/`
	redacted := policy.Text(input)
	for secretID, secret := range []string{"abc123", "xyz", "live-value"} {
		if strings.Contains(redacted, secret) {
			t.Errorf("redaction output contains secret %d", secretID)
		}
	}
	assert.Contains(t, redacted, Replacement, "replacement missing")
}

func TestTextRedactsStructuredCredentialAssignments(t *testing.T) {
	input := strings.Join([]string{
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"aws_session_token=session-value-123456",
		"client-key-data: LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0t",
		`"private_key_id":"private-key-id-value"`,
		"service-account-json=base64-service-account-value",
	}, "\n")
	redacted := DefaultPolicy(nil).Text(input)
	for secretID, secret := range []string{
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"session-value-123456",
		"LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0t",
		"private-key-id-value",
		"base64-service-account-value",
	} {
		if strings.Contains(redacted, secret) {
			t.Errorf("structured credential %d leaked", secretID)
		}
	}
}

func TestTextRedactsStandaloneCredentialsAndTruncatedPrivateKey(t *testing.T) {
	input := strings.Join([]string{
		"ghp_" + "abcdefghijklmnopqrstuvwxyz123456",
		"github_pat_" + "abcdefghijklmnopqrstuvwxyz123456",
		"xoxb-" + "1234567890-" + "abcdefghijklmnop",
		"AKIA" + "ABCDEFGHIJKLMNOP",
		"ASIA" + "ABCDEFGHIJKLMNOP",
		"sk-proj-" + "abcdefghijklmnopqrstuvwxyz1234567890",
		"glpat-" + "abcdefghijklmnopqrstuvwxyz",
		"sk_live_" + "abcdefghijklmnopqrstuvwxyz",
		"npm_" + "abcdefghijklmnopqrstuvwxyz",
		"AIza" + "abcdefghijklmnopqrstuvwxyz1234567890",
		"eyJ" + "abcdefghijk.abcdefghijkl.abcdefghijkl",
		"postgres://alice:database-password@example.invalid/db",
		"-----BEGIN PRIVATE KEY-----\npartial-key-without-end-marker",
	}, "\n")
	redacted := DefaultPolicy(nil).Text(input)
	for secretID, secret := range []string{
		"ghp_", "github_pat_", "xoxb-", "AKIA", "ASIA", "sk-proj-", "glpat-", "sk_live_", "npm_", "AIza",
		"eyJ", "database-password", "partial-key",
	} {
		if strings.Contains(redacted, secret) {
			t.Errorf("secret class %d leaked", secretID)
		}
	}
}

func TestDisabledPolicyDoesNotModifyInput(t *testing.T) {
	p := Policy{Enabled: false}
	const input = "password=hunter2"
	if got := p.Text(input); got != input {
		t.Fatalf("Text() = %q, want original", got)
	}
}
