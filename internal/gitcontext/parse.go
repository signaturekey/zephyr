package gitcontext

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func parsePorcelainV2(data []byte) (WorktreeStatus, error) {
	tokens := splitNUL(data)
	status := WorktreeStatus{
		Entries:   make([]StatusEntry, 0, len(tokens)),
		Untracked: make([]UntrackedFile, 0),
	}
	for i := 0; i < len(tokens); i++ {
		record := tokens[i]
		if record == "" {
			continue
		}
		var entry StatusEntry
		switch record[0] {
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 {
				return WorktreeStatus{}, fmt.Errorf("parse ordinary status record %q", record)
			}
			entry = statusEntry(fields[8], fields[1], fields[2])
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || i+1 >= len(tokens) {
				return WorktreeStatus{}, fmt.Errorf("parse rename status record %q", record)
			}
			entry = statusEntry(fields[9], fields[1], fields[2])
			entry.PreviousPath = tokens[i+1]
			entry.Generated = entry.Generated || isGeneratedPath(entry.PreviousPath)
			entry.Vendor = entry.Vendor || isVendorPath(entry.PreviousPath)
			entry.Restricted = entry.Restricted || isRestrictedPath(entry.PreviousPath)
			i++
		case 'u':
			fields := strings.SplitN(record, " ", 11)
			if len(fields) != 11 {
				return WorktreeStatus{}, fmt.Errorf("parse unmerged status record %q", record)
			}
			entry = statusEntry(fields[10], fields[1], fields[2])
			entry.Unmerged = true
		case '?':
			if len(record) < 3 || record[1] != ' ' {
				return WorktreeStatus{}, fmt.Errorf("parse untracked status record %q", record)
			}
			entry = statusEntry(record[2:], "??", "")
			entry.Untracked = true
			status.Untracked = append(status.Untracked, UntrackedFile{
				Path:       entry.Path,
				Generated:  entry.Generated,
				Vendor:     entry.Vendor,
				Restricted: entry.Restricted,
			})
		case '!':
			if len(record) < 3 || record[1] != ' ' {
				return WorktreeStatus{}, fmt.Errorf("parse ignored status record %q", record)
			}
			entry = statusEntry(record[2:], "!!", "")
			entry.Ignored = true
		default:
			return WorktreeStatus{}, fmt.Errorf("unknown porcelain v2 record %q", record)
		}
		status.Entries = append(status.Entries, entry)
	}
	sort.Slice(status.Entries, func(i, j int) bool {
		if status.Entries[i].Path == status.Entries[j].Path {
			return status.Entries[i].PreviousPath < status.Entries[j].PreviousPath
		}
		return status.Entries[i].Path < status.Entries[j].Path
	})
	sort.Slice(status.Untracked, func(i, j int) bool {
		return status.Untracked[i].Path < status.Untracked[j].Path
	})
	return status, nil
}

func statusEntry(path, xy, submodule string) StatusEntry {
	entry := StatusEntry{
		Path:           path,
		SubmoduleState: normalizedSubmoduleState(submodule),
		Generated:      isGeneratedPath(path),
		Vendor:         isVendorPath(path),
		Restricted:     isRestrictedPath(path),
	}
	if len(xy) >= 2 {
		if xy[0] != '.' {
			entry.IndexStatus = string(xy[0])
		}
		if xy[1] != '.' {
			entry.WorktreeStatus = string(xy[1])
		}
	}
	return entry
}

func normalizedSubmoduleState(value string) string {
	if value == "N..." || value == "" {
		return ""
	}
	return value
}

func parseRawChanges(data []byte) ([]FileChange, error) {
	tokens := splitNUL(data)
	changes := make([]FileChange, 0, len(tokens)/2)
	for i := 0; i < len(tokens); {
		header := tokens[i]
		i++
		if header == "" {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 5 || len(fields[0]) < 2 || fields[0][0] != ':' {
			return nil, fmt.Errorf("parse raw diff header %q", header)
		}
		if i >= len(tokens) {
			return nil, fmt.Errorf("raw diff header %q is missing a path", header)
		}
		gitStatus := fields[4]
		statusCode := gitStatus[:1]
		firstPath := tokens[i]
		i++
		path := firstPath
		previousPath := ""
		if statusCode == "R" || statusCode == "C" {
			if i >= len(tokens) {
				return nil, fmt.Errorf("raw diff header %q is missing its destination path", header)
			}
			previousPath = firstPath
			path = tokens[i]
			i++
		}
		similarity := 0
		if len(gitStatus) > 1 {
			parsed, err := strconv.Atoi(gitStatus[1:])
			if err != nil {
				return nil, fmt.Errorf("parse similarity score %q: %w", gitStatus, err)
			}
			similarity = parsed
		}
		change := FileChange{
			Path:         path,
			PreviousPath: previousPath,
			Status:       changeStatus(statusCode),
			GitStatus:    gitStatus,
			Similarity:   similarity,
			OldMode:      strings.TrimPrefix(fields[0], ":"),
			NewMode:      fields[1],
			Generated:    isGeneratedPath(path) || isGeneratedPath(previousPath),
			Vendor:       isVendorPath(path) || isVendorPath(previousPath),
			Restricted:   isRestrictedPath(path) || isRestrictedPath(previousPath),
		}
		change.Submodule = change.OldMode == "160000" || change.NewMode == "160000"
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].PreviousPath < changes[j].PreviousPath
		}
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

type numstat struct {
	path         string
	previousPath string
	insertions   *int
	deletions    *int
	binary       bool
}

func parseNumstat(data []byte) ([]numstat, error) {
	tokens := splitNUL(data)
	stats := make([]numstat, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		record := tokens[i]
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("parse numstat record %q", record)
		}
		stat := numstat{}
		if fields[0] == "-" && fields[1] == "-" {
			stat.binary = true
		} else {
			insertions, err := strconv.Atoi(fields[0])
			if err != nil {
				return nil, fmt.Errorf("parse numstat insertions %q: %w", fields[0], err)
			}
			deletions, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("parse numstat deletions %q: %w", fields[1], err)
			}
			stat.insertions = &insertions
			stat.deletions = &deletions
		}
		if fields[2] == "" {
			if i+2 >= len(tokens) {
				return nil, fmt.Errorf("renamed numstat record %q is missing paths", record)
			}
			stat.previousPath = tokens[i+1]
			stat.path = tokens[i+2]
			i += 2
		} else {
			stat.path = fields[2]
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

func attachNumstat(changes []FileChange, stats []numstat) []FileChange {
	byPath := make(map[string][]numstat, len(stats))
	for _, stat := range stats {
		key := stat.path + "\x00" + stat.previousPath
		byPath[key] = append(byPath[key], stat)
	}
	for i := range changes {
		key := changes[i].Path + "\x00" + changes[i].PreviousPath
		candidates := byPath[key]
		if len(candidates) == 0 {
			for candidateKey, values := range byPath {
				if strings.HasPrefix(candidateKey, changes[i].Path+"\x00") && len(values) > 0 {
					candidates = values
					break
				}
			}
		}
		if len(candidates) == 0 {
			continue
		}
		changes[i].Insertions = candidates[0].insertions
		changes[i].Deletions = candidates[0].deletions
		changes[i].Binary = candidates[0].binary
	}
	return changes
}

func changeStatus(code string) ChangeStatus {
	switch code {
	case "A":
		return ChangeAdded
	case "M":
		return ChangeModified
	case "D":
		return ChangeDeleted
	case "R":
		return ChangeRenamed
	case "C":
		return ChangeCopied
	case "T":
		return ChangeTypeChanged
	case "U":
		return ChangeUnmerged
	default:
		return ChangeUnknown
	}
}

func splitNUL(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	result := make([]string, len(parts))
	for i := range parts {
		result[i] = string(parts[i])
	}
	return result
}
