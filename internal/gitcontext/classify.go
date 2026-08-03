package gitcontext

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var restrictedGitGlobs = []string{
	".env*",
	"**/.env*",
	"*.pem",
	"**/*.pem",
	"*.key",
	"**/*.key",
	"*.p12",
	"**/*.p12",
	"*.pfx",
	"**/*.pfx",
	"*secret*",
	"**/*secret*",
	"*credential*",
	"**/*credential*",
	"id_rsa*",
	"**/id_rsa*",
	"id_ed25519*",
	"**/id_ed25519*",
	".netrc",
	"**/.netrc",
}

var generatedGitGlobs = []string{
	"generated/**",
	"**/generated/**",
	"internal/generated/**",
	"**/*.gen.go",
	"**/*_generated.go",
	"**/*.pb.go",
	"**/zz_generated.*",
	"**/__generated__/**",
	"**/*.gen.ts",
	"**/*.generated.ts",
	"**/*.gen.tsx",
	"**/*.generated.tsx",
}

var vendorGitGlobs = []string{
	"vendor/**",
	"**/vendor/**",
}

func isRestrictedPath(value string) bool {
	if value == "" {
		return false
	}
	normalized := strings.ToLower(path.Clean(strings.ReplaceAll(value, "\\", "/")))
	base := path.Base(normalized)
	if strings.HasPrefix(base, ".env") || base == ".netrc" ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") {
		return true
	}
	if strings.Contains(base, "secret") || strings.Contains(base, "credential") {
		return true
	}
	switch path.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx":
		return true
	default:
		return false
	}
}

func isGeneratedPath(value string) bool {
	if value == "" {
		return false
	}
	normalized := strings.ToLower(path.Clean(strings.ReplaceAll(value, "\\", "/")))
	base := path.Base(normalized)
	if normalized == "generated" || strings.HasPrefix(normalized, "generated/") ||
		strings.Contains(normalized, "/generated/") || strings.Contains(normalized, "/__generated__/") ||
		strings.HasPrefix(normalized, "__generated__/") || strings.HasPrefix(normalized, "internal/generated/") {
		return true
	}
	return strings.HasSuffix(base, ".gen.go") || strings.HasSuffix(base, "_generated.go") ||
		strings.HasSuffix(base, ".pb.go") || strings.HasSuffix(base, ".gen.ts") ||
		strings.HasSuffix(base, ".generated.ts") || strings.HasSuffix(base, ".gen.tsx") ||
		strings.HasSuffix(base, ".generated.tsx") || strings.HasPrefix(base, "zz_generated.")
}

func isVendorPath(value string) bool {
	if value == "" {
		return false
	}
	normalized := strings.ToLower(path.Clean(strings.ReplaceAll(value, "\\", "/")))
	return normalized == "vendor" || strings.HasPrefix(normalized, "vendor/") || strings.Contains(normalized, "/vendor/")
}

func applyContentPolicy(changes []FileChange, includeGenerated, includeVendor bool) []FileChange {
	for i := range changes {
		changes[i].ContentIncluded = true
		switch {
		case changes[i].Restricted:
			changes[i].ContentIncluded = false
			changes[i].ExclusionReason = "restricted"
		case changes[i].Binary:
			changes[i].ContentIncluded = false
			changes[i].ExclusionReason = "binary"
		case changes[i].Generated && !includeGenerated:
			changes[i].ContentIncluded = false
			changes[i].ExclusionReason = "generated"
		case changes[i].Vendor && !includeVendor:
			changes[i].ContentIncluded = false
			changes[i].ExclusionReason = "vendor"
		}
	}
	return changes
}

func patchPathspecs(changes []FileChange, includeGenerated, includeVendor bool, restrictedPatterns []string) []string {
	pathspecs := []string{"."}
	for _, pattern := range restrictedGitGlobs {
		pathspecs = append(pathspecs, ":(top,exclude,glob)"+pattern)
	}
	for _, pattern := range restrictedPatterns {
		pathspecs = append(pathspecs, ":(top,exclude,glob)"+filepath.ToSlash(pattern))
	}
	if !includeGenerated {
		for _, pattern := range generatedGitGlobs {
			pathspecs = append(pathspecs, ":(top,exclude,glob)"+pattern)
		}
	}
	if !includeVendor {
		for _, pattern := range vendorGitGlobs {
			pathspecs = append(pathspecs, ":(top,exclude,glob)"+pattern)
		}
	}
	seen := make(map[string]struct{})
	for _, change := range changes {
		if !change.Binary && !change.Restricted {
			continue
		}
		for _, value := range []string{change.Path, change.PreviousPath} {
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			pathspecs = append(pathspecs, ":(top,exclude,literal)"+value)
		}
	}
	return pathspecs
}

func safeStatePathspecs(status WorktreeStatus, restrictedPatterns []string) []string {
	pathspecs := []string{"."}
	for _, pattern := range restrictedGitGlobs {
		pathspecs = append(pathspecs, ":(top,exclude,glob)"+pattern)
	}
	for _, pattern := range restrictedPatterns {
		pathspecs = append(pathspecs, ":(top,exclude,glob)"+filepath.ToSlash(pattern))
	}
	seen := make(map[string]struct{})
	for _, entry := range status.Entries {
		if !entry.Restricted {
			continue
		}
		for _, value := range []string{entry.Path, entry.PreviousPath} {
			if value == "" {
				continue
			}
			value = filepath.ToSlash(value)
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			pathspecs = append(pathspecs, ":(top,exclude,literal)"+value)
		}
	}
	return pathspecs
}

func validateRestrictedPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if _, err := doublestar.PathMatch(filepath.ToSlash(pattern), "validation/path"); err != nil {
		return fmt.Errorf("invalid glob %q: %w", pattern, err)
	}
	return nil
}

func restrictedByPatterns(value string, patterns []string) bool {
	if value == "" {
		return false
	}
	value = filepath.ToSlash(path.Clean(strings.ReplaceAll(value, "\\", "/")))
	for _, pattern := range patterns {
		matched, err := doublestar.PathMatch(filepath.ToSlash(pattern), value)
		if err != nil || matched {
			return true
		}
	}
	return false
}

func applyRestrictedPatterns(changes []FileChange, patterns []string) []FileChange {
	for index := range changes {
		if restrictedByPatterns(changes[index].Path, patterns) || restrictedByPatterns(changes[index].PreviousPath, patterns) {
			changes[index].Restricted = true
		}
	}
	return changes
}

func applyStatusRestrictedPatterns(status WorktreeStatus, patterns []string) WorktreeStatus {
	for index := range status.Entries {
		if restrictedByPatterns(status.Entries[index].Path, patterns) || restrictedByPatterns(status.Entries[index].PreviousPath, patterns) {
			status.Entries[index].Restricted = true
		}
	}
	for index := range status.Untracked {
		if restrictedByPatterns(status.Untracked[index].Path, patterns) {
			status.Untracked[index].Restricted = true
		}
	}
	return status
}

func calculateStats(changes []FileChange) DiffStats {
	stats := DiffStats{Files: len(changes)}
	for _, change := range changes {
		if change.Insertions != nil {
			stats.Insertions += *change.Insertions
		}
		if change.Deletions != nil {
			stats.Deletions += *change.Deletions
		}
		if change.Binary {
			stats.Binary++
		}
		if change.Generated {
			stats.Generated++
		}
		if change.Vendor {
			stats.Vendor++
		}
		if change.Restricted {
			stats.Restricted++
		}
		if change.Submodule {
			stats.Submodules++
		}
	}
	return stats
}
