package gitcontext

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maximumAllowedUntrackedBytes int64 = 16 * 1024 * 1024

func collectUntracked(
	repository string,
	status *WorktreeStatus,
	options Options,
	maximum int64,
) (string, []string, error) {
	var patches strings.Builder
	limitations := make([]string, 0)
	for i := range status.Untracked {
		file := &status.Untracked[i]
		file.ContentIncluded = false
		switch {
		case file.Restricted:
			file.ExclusionReason = "restricted"
			continue
		case file.Generated && !options.IncludeGenerated:
			file.ExclusionReason = "generated"
			continue
		case file.Vendor && !options.IncludeVendor:
			file.ExclusionReason = "vendor"
			continue
		}

		content, mode, truncated, reason, err := readSafeUntracked(repository, file.Path, maximum)
		if err != nil {
			return "", nil, err
		}
		if reason != "" {
			file.ExclusionReason = reason
			file.Binary = reason == "binary"
			limitations = append(limitations, fmt.Sprintf("untracked %s content excluded: %s", reason, file.Path))
			continue
		}
		if containsLikelySecret(content) {
			file.Restricted = true
			file.ExclusionReason = "secret-like-content"
			limitations = append(limitations, fmt.Sprintf("untracked secret-like content excluded: %s", file.Path))
			continue
		}
		file.ContentIncluded = true
		file.Truncated = truncated
		patches.WriteString(syntheticAddedPatch(file.Path, mode, content, truncated, maximum))
		if truncated {
			limitations = append(limitations, fmt.Sprintf("untracked content truncated after %d bytes: %s", maximum, file.Path))
		}
	}

	byPath := make(map[string]UntrackedFile, len(status.Untracked))
	for _, file := range status.Untracked {
		byPath[file.Path] = file
	}
	for i := range status.Entries {
		if metadata, exists := byPath[status.Entries[i].Path]; exists {
			status.Entries[i].Generated = metadata.Generated
			status.Entries[i].Vendor = metadata.Vendor
			status.Entries[i].Restricted = metadata.Restricted
		}
	}
	return patches.String(), limitations, nil
}

func readSafeUntracked(repository, relative string, maximum int64) ([]byte, os.FileMode, bool, string, error) {
	absolute, err := repositoryPath(repository, relative)
	if err != nil {
		return nil, 0, false, "", err
	}
	symlink, err := hasSymlinkComponent(repository, relative)
	if err != nil {
		return nil, 0, false, "", err
	}
	if symlink {
		return nil, 0, false, "symlink", nil
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, "missing", nil
	}
	if err != nil {
		return nil, 0, false, "", fmt.Errorf("inspect untracked file %q: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return nil, info.Mode(), false, "non-regular", nil
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, 0, false, "", fmt.Errorf("open untracked file %q: %w", relative, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, false, "", fmt.Errorf("stat open untracked file %q: %w", relative, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, openedInfo.Mode(), false, "changed-during-read", nil
	}
	symlink, err = hasSymlinkComponent(repository, relative)
	if err != nil {
		return nil, 0, false, "", err
	}
	if symlink {
		return nil, openedInfo.Mode(), false, "changed-during-read", nil
	}
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, 0, false, "", fmt.Errorf("read untracked file %q: %w", relative, err)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return nil, 0, false, "", fmt.Errorf("recheck untracked file %q: %w", relative, err)
	}
	if finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
		return nil, finalInfo.Mode(), false, "changed-during-read", nil
	}
	truncated := int64(len(content)) > maximum
	if truncated {
		content = content[:maximum]
		for len(content) > 0 && !utf8.Valid(content) {
			content = content[:len(content)-1]
		}
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return nil, finalInfo.Mode(), false, "binary", nil
	}
	return content, finalInfo.Mode(), truncated, "", nil
}

func hasSymlinkComponent(repository, relative string) (bool, error) {
	current := repository
	for _, component := range strings.Split(filepath.Clean(filepath.FromSlash(relative)), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return false, fmt.Errorf("inspect untracked path component %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func containsLikelySecret(content []byte) bool {
	lower := strings.ToLower(string(content))
	for _, marker := range []string{
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"-----begin openssh private key-----",
		"-----begin ec private key-----",
		"aws_secret_access_key",
		"client_secret=",
		"client_secret:",
		"api_key=",
		"api_key:",
		"password=",
		"password:",
		"access_token=",
		"refresh_token=",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func syntheticAddedPatch(relative string, mode os.FileMode, content []byte, truncated bool, maximum int64) string {
	gitMode := "100644"
	if mode&0o111 != 0 {
		gitMode = "100755"
	}
	aPath := quotedPatchPath("a/" + filepath.ToSlash(relative))
	bPath := quotedPatchPath("b/" + filepath.ToSlash(relative))
	var builder strings.Builder
	fmt.Fprintf(&builder, "diff --git %s %s\n", aPath, bPath)
	fmt.Fprintf(&builder, "new file mode %s\n", gitMode)
	if truncated {
		fmt.Fprintf(&builder, "# zephyr: untracked content truncated after %d bytes\n", maximum)
	}
	builder.WriteString("--- /dev/null\n")
	fmt.Fprintf(&builder, "+++ %s\n", bPath)
	if len(content) == 0 {
		return builder.String()
	}
	lineCount := bytes.Count(content, []byte{'\n'})
	if !bytes.HasSuffix(content, []byte{'\n'}) {
		lineCount++
	}
	fmt.Fprintf(&builder, "@@ -0,0 +1,%d @@\n", lineCount)
	body := content
	if bytes.HasSuffix(body, []byte{'\n'}) {
		body = body[:len(body)-1]
	}
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		builder.WriteByte('+')
		builder.Write(line)
		builder.WriteByte('\n')
	}
	if !bytes.HasSuffix(content, []byte{'\n'}) {
		builder.WriteString("\\ No newline at end of file\n")
	}
	return builder.String()
}

func quotedPatchPath(value string) string {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("/._-", char) {
			continue
		}
		return strconv.Quote(value)
	}
	return value
}
