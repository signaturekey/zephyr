package evidence

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

type lineRange struct {
	start int
	end   int
}

type diffHunk struct {
	path  string
	start int
	end   int
	text  string
}

func extractDiffHunks(diff string) []diffHunk {
	type pendingHunk struct {
		path       string
		start, end int
		textStart  int
		header     string
	}
	var result []diffHunk
	oldPath, newPath, currentPath := "", "", ""
	fileHeaderStart := 0
	fileHeader := ""
	expectOldPath, expectNewPath := true, false
	deleted := false
	oldLine, newLine := 0, 0
	var active *pendingHunk

	finish := func(end int) {
		if active == nil || active.end < active.start {
			active = nil
			return
		}
		result = append(result, diffHunk{path: active.path, start: active.start, end: active.end, text: active.header + diff[active.textStart:end]})
		active = nil
	}

	for offset := 0; offset < len(diff); {
		next := strings.IndexByte(diff[offset:], '\n')
		lineEnd := len(diff)
		if next >= 0 {
			lineEnd = offset + next + 1
		}
		line := strings.TrimSuffix(diff[offset:lineEnd], "\n")
		line = strings.TrimSuffix(line, "\r")

		switch {
		case strings.HasPrefix(line, "diff --git "):
			finish(offset)
			oldPath, newPath, currentPath = "", "", ""
			deleted = false
			fileHeader = ""
			expectOldPath, expectNewPath = true, false
		case expectOldPath && strings.HasPrefix(line, "--- "):
			finish(offset)
			oldPath = parsePatchPath(strings.TrimPrefix(line, "--- "), "a/")
			fileHeaderStart = offset
			fileHeader = ""
			expectOldPath, expectNewPath = false, true
		case expectNewPath && strings.HasPrefix(line, "+++ "):
			newPath = parsePatchPath(strings.TrimPrefix(line, "+++ "), "b/")
			deleted = newPath == "" && oldPath != ""
			if deleted {
				currentPath = oldPath
			} else {
				currentPath = newPath
			}
			fileHeader = diff[fileHeaderStart:lineEnd]
			expectNewPath = false
		case currentPath != "" && strings.HasPrefix(line, "@@ "):
			finish(offset)
			matches := hunkHeader.FindStringSubmatch(line)
			if matches == nil {
				break
			}
			var err error
			oldLine, err = strconv.Atoi(matches[1])
			if err != nil {
				break
			}
			newLine, err = strconv.Atoi(matches[3])
			if err != nil {
				break
			}
			active = &pendingHunk{path: currentPath, start: chooseLine(deleted, oldLine, newLine), textStart: offset, header: fileHeader}
		case active != nil && strings.HasPrefix(line, " "):
			active.end = chooseLine(deleted, oldLine, newLine)
			oldLine++
			newLine++
		case active != nil && strings.HasPrefix(line, "+"):
			if !deleted {
				active.end = newLine
			}
			newLine++
		case active != nil && strings.HasPrefix(line, "-"):
			if deleted {
				active.end = oldLine
			}
			oldLine++
		case active != nil && strings.HasPrefix(line, `\ No newline at end of file`):
		default:
			expectNewPath = false
			finish(offset)
		}
		offset = lineEnd
	}
	finish(len(diff))
	return result
}

func chooseLine(deleted bool, oldLine, newLine int) int {
	if deleted {
		return oldLine
	}
	return newLine
}

func diffLineIndex(diff string) map[string][]lineRange {
	linesByPath := make(map[string]map[int]struct{})
	oldPath := ""
	newPath := ""
	currentPath := ""
	expectOldPath, expectNewPath := true, false
	oldLine := 0
	newLine := 0
	deleted := false
	inHunk := false

	add := func(path string, line int) {
		if path == "" || line < 1 {
			return
		}
		if linesByPath[path] == nil {
			linesByPath[path] = make(map[int]struct{})
		}
		linesByPath[path][line] = struct{}{}
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldPath, newPath, currentPath = "", "", ""
			inHunk = false
			expectOldPath, expectNewPath = true, false
		case expectOldPath && strings.HasPrefix(line, "--- "):
			oldPath = parsePatchPath(strings.TrimPrefix(line, "--- "), "a/")
			inHunk = false
			expectOldPath, expectNewPath = false, true
		case (expectNewPath || currentPath == "") && strings.HasPrefix(line, "+++ "):
			newPath = parsePatchPath(strings.TrimPrefix(line, "+++ "), "b/")
			deleted = newPath == "" && oldPath != ""
			if deleted {
				currentPath = oldPath
			} else {
				currentPath = newPath
			}
			inHunk = false
			expectNewPath = false
		case currentPath != "" && strings.HasPrefix(line, "@@ "):
			matches := hunkHeader.FindStringSubmatch(line)
			if matches == nil {
				inHunk = false
				continue
			}
			var err error
			oldLine, err = strconv.Atoi(matches[1])
			if err != nil {
				inHunk = false
				continue
			}
			newLine, err = strconv.Atoi(matches[3])
			inHunk = err == nil
		case inHunk && strings.HasPrefix(line, " "):
			if deleted {
				add(currentPath, oldLine)
			} else {
				add(currentPath, newLine)
			}
			oldLine++
			newLine++
		case inHunk && strings.HasPrefix(line, "+"):
			if !deleted {
				add(currentPath, newLine)
			}
			newLine++
		case inHunk && strings.HasPrefix(line, "-"):
			if deleted {
				add(currentPath, oldLine)
			}
			oldLine++
		case inHunk && strings.HasPrefix(line, `\ No newline at end of file`):
		default:
			if currentPath != "" {
				expectNewPath = false
			}
			inHunk = false
		}
	}

	result := make(map[string][]lineRange, len(linesByPath))
	for path, lineSet := range linesByPath {
		lines := make([]int, 0, len(lineSet))
		for line := range lineSet {
			lines = append(lines, line)
		}
		sort.Ints(lines)
		for _, line := range lines {
			last := len(result[path]) - 1
			if last >= 0 && line == result[path][last].end+1 {
				result[path][last].end = line
				continue
			}
			result[path] = append(result[path], lineRange{start: line, end: line})
		}
	}
	return result
}

func diffContainsLineRange(diff, expectedPath string, start, end int) bool {
	for _, candidate := range diffLineIndex(diff)[expectedPath] {
		if start >= candidate.start && end <= candidate.end {
			return true
		}
	}
	return false
}

func diffContainsEvidenceCode(diff, expectedPath, evidence string) bool {
	corpus := normalizeCode(diffVisibleContent(diff, expectedPath))
	if corpus == "" {
		return false
	}
	for _, fragment := range strings.Split(evidence, ";") {
		fragment = normalizeCode(fragment)
		if fragment != "" && !strings.Contains(corpus, fragment) && !tokenSequenceContains(corpus, fragment, 20) {
			return false
		}
	}
	return true
}

func diffVisibleContent(diff, expectedPath string) string {
	oldPath, newPath, currentPath := "", "", ""
	deleted := false
	inHunk := false
	var builder strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldPath, newPath, currentPath = "", "", ""
			inHunk = false
		case strings.HasPrefix(line, "--- "):
			oldPath = parsePatchPath(strings.TrimPrefix(line, "--- "), "a/")
			inHunk = false
		case strings.HasPrefix(line, "+++ "):
			newPath = parsePatchPath(strings.TrimPrefix(line, "+++ "), "b/")
			deleted = newPath == "" && oldPath != ""
			if deleted {
				currentPath = oldPath
			} else {
				currentPath = newPath
			}
			inHunk = false
		case currentPath != "" && strings.HasPrefix(line, "@@ "):
			inHunk = hunkHeader.MatchString(line)
		case inHunk && currentPath == expectedPath && strings.HasPrefix(line, " "):
			builder.WriteString(line[1:])
			builder.WriteByte('\n')
		case inHunk && currentPath == expectedPath && !deleted && strings.HasPrefix(line, "+"):
			builder.WriteString(line[1:])
			builder.WriteByte('\n')
		case inHunk && currentPath == expectedPath && deleted && strings.HasPrefix(line, "-"):
			builder.WriteString(line[1:])
			builder.WriteByte('\n')
		case inHunk && strings.HasPrefix(line, "-"):
		case inHunk && deleted && strings.HasPrefix(line, "+"):
		case inHunk && strings.HasPrefix(line, `\ No newline at end of file`):
		default:
			inHunk = false
		}
	}
	return builder.String()
}

func normalizeCode(value string) string { return strings.Join(strings.Fields(value), " ") }

var evidenceToken = regexp.MustCompile(`[[:alnum:]_./]+`)

func tokenSequenceContains(corpus, fragment string, maximumWindow int) bool {
	corpusTokens := evidenceToken.FindAllString(corpus, -1)
	fragmentTokens := evidenceToken.FindAllString(fragment, -1)
	if len(fragmentTokens) == 0 {
		return false
	}
	for start, token := range corpusTokens {
		if token != fragmentTokens[0] {
			continue
		}
		matched := 1
		limit := min(len(corpusTokens), start+maximumWindow)
		for index := start + 1; index < limit && matched < len(fragmentTokens); index++ {
			if corpusTokens[index] == fragmentTokens[matched] {
				matched++
			}
		}
		if matched == len(fragmentTokens) {
			return true
		}
	}
	return false
}

func parsePatchPath(value, prefix string) string {
	value = strings.TrimSuffix(value, "\r")
	if strings.HasPrefix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return ""
		}
		value = decoded
	} else if index := strings.IndexByte(value, '\t'); index >= 0 {
		value = value[:index]
	}
	if value == "/dev/null" {
		return ""
	}
	value = strings.TrimPrefix(value, prefix)
	return strings.ReplaceAll(value, "\\", "/")
}
