package redaction

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const Replacement = "[REDACTED]"

var defaultDenyPatterns = []string{
	"**/.env",
	"**/.env.*",
	"**/.env*",
	"**/*credential*",
	"**/*secret*",
	"**/*.key",
	"**/*.pem",
	"**/*.p12",
	"**/*.pfx",
	"**/.netrc",
	"**/id_rsa*",
	"**/id_ed25519*",
}

var sensitiveTextPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`), Replacement},
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s"']+`), `${1}` + Replacement},
	{regexp.MustCompile(`(?i)((?:"|')?(?:aws[_-]?(?:secret[_-]?access[_-]?key|session[_-]?token)|client[_-]?key[_-]?data|private[_-]?key(?:[_-]?(?:data|id))?|service[_-]?account[_-]?(?:key|json))(?:"|')?\s*[:=]\s*(?:"|')?)[^"'\s,;}&]+`), `${1}` + Replacement},
	{regexp.MustCompile(`(?i)((?:"|')?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd|secret|token)(?:"|')?\s*[:=]\s*(?:"|')?)[^"'\s,;}&]+`), `${1}` + Replacement},
	{regexp.MustCompile(`(?i)((?:https?|postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://[^\s/:]+:)[^@\s]+(@)`), `${1}` + Replacement + `${2}`},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`), Replacement},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), Replacement},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), Replacement},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}\b`), Replacement},
	{regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,}\b`), Replacement},
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`), Replacement},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`), Replacement},
	{regexp.MustCompile(`\bxox[a-z]-[A-Za-z0-9-]{10,}\b`), Replacement},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), Replacement},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), Replacement},
}

type Policy struct {
	Enabled      bool
	DenyPatterns []string
	baseline     int
}

func DefaultPolicy(extra []string) Policy {
	patterns := append([]string(nil), defaultDenyPatterns...)
	patterns = append(patterns, extra...)
	return Policy{Enabled: true, DenyPatterns: patterns, baseline: len(defaultDenyPatterns)}
}

func (p Policy) PathDenied(path string) bool {
	if !p.Enabled {
		return false
	}

	normalized := filepath.ToSlash(filepath.Clean(path))
	normalized = strings.TrimPrefix(normalized, "./")
	for index, pattern := range p.DenyPatterns {
		candidate := normalized
		pattern = filepath.ToSlash(pattern)
		if index < p.baseline {
			candidate = strings.ToLower(candidate)
			pattern = strings.ToLower(pattern)
		}
		matched, err := doublestar.PathMatch(pattern, candidate)
		if err != nil || matched {
			return true
		}
	}
	return false
}

func (p Policy) Text(value string) string {
	if !p.Enabled || value == "" {
		return value
	}

	redacted := value
	for _, item := range sensitiveTextPatterns {
		redacted = item.pattern.ReplaceAllString(redacted, item.replacement)
	}
	return redacted
}

func (p Policy) Strings(values []string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = p.Text(value)
	}
	return result
}
