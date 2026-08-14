package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	OwnerMarkerName = ".zephyr-codex-owned"
	OwnerMarkerText = "zephyr-codex-owned-v1"
)

type RetentionPolicy struct {
	OperationMaxAge time.Duration
	OperationMax    int
	CacheMaxAge     time.Duration
	CacheMax        int
}

type CoverageCollection string

const (
	CoverageOperations CoverageCollection = "operations"
	CoverageCache      CoverageCollection = "cache"
)

type CoverageReason string

const (
	CoverageForeignEntry      CoverageReason = "foreign-entry"
	CoverageMalformedMarker   CoverageReason = "malformed-marker"
	CoveragePrivateRetained   CoverageReason = "private-retained"
	CoverageSymlinkEntry      CoverageReason = "symlink-entry"
	CoverageIncompleteEntry   CoverageReason = "incomplete-entry"
	CoverageMetadataUncertain CoverageReason = "metadata-unavailable"
)

type CoverageEvent struct {
	Collection  CoverageCollection `json:"collection"`
	Reason      CoverageReason     `json:"reason"`
	EntrySHA256 string             `json:"entry_sha256"`
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		OperationMaxAge: 14 * 24 * time.Hour,
		OperationMax:    50,
		CacheMaxAge:     30 * 24 * time.Hour,
		CacheMax:        8,
	}
}

func Prune(root string, policy RetentionPolicy, now time.Time) ([]CoverageEvent, error) {
	if policy == (RetentionPolicy{}) {
		policy = DefaultRetentionPolicy()
	}
	if policy.OperationMaxAge < 0 || policy.OperationMax < 0 || policy.CacheMaxAge < 0 || policy.CacheMax < 0 {
		return nil, errors.New("retention limits must not be negative")
	}
	if now.IsZero() {
		return nil, errors.New("retention time is required")
	}
	rawRoots := Roots{
		DriverRoot: root,
		Operation:  filepath.Join(root, "operations"),
		RunRoot:    filepath.Join(root, "runs"),
		CacheRoot:  filepath.Join(root, "cache"),
	}
	if err := ValidateManagedRoots(rawRoots); err != nil {
		return nil, fmt.Errorf("validate supplied retention root: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect retention root: %w", err)
	}
	if err == nil && rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("retention root must not be a symlink")
	}
	root, err = canonicalPath(root)
	if err != nil {
		return nil, fmt.Errorf("canonicalize retention root: %w", err)
	}
	roots := Roots{
		DriverRoot: root,
		Operation:  filepath.Join(root, "operations"),
		RunRoot:    filepath.Join(root, "runs"),
		CacheRoot:  filepath.Join(root, "cache"),
	}
	if err := ValidateManagedRoots(roots); err != nil {
		return nil, fmt.Errorf("validate retention root: %w", err)
	}
	var coverage []CoverageEvent
	for _, target := range []struct {
		name       CoverageCollection
		maxAge     time.Duration
		maxEntries int
		completed  bool
	}{
		{name: CoverageOperations, maxAge: policy.OperationMaxAge, maxEntries: policy.OperationMax, completed: true},
		{name: CoverageCache, maxAge: policy.CacheMaxAge, maxEntries: policy.CacheMax},
	} {
		events, err := pruneEntries(filepath.Join(root, string(target.name)), target.name, target.maxAge, target.maxEntries, now, target.completed)
		coverage = append(coverage, events...)
		if err != nil {
			return coverage, fmt.Errorf("prune %s: %w", target.name, err)
		}
	}
	return coverage, nil
}

type retentionEntry struct {
	path     string
	modified time.Time
}

func pruneEntries(root string, collection CoverageCollection, maxAge time.Duration, maxEntries int, now time.Time, requireCompleted bool) ([]CoverageEvent, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	coverage := make([]CoverageEvent, 0)
	owned := make([]retentionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			coverage = append(coverage, coverageEvent(collection, CoverageSymlinkEntry, entry.Name()))
			continue
		}
		if !entry.IsDir() {
			coverage = append(coverage, coverageEvent(collection, CoverageForeignEntry, entry.Name()))
			continue
		}
		path := filepath.Join(root, entry.Name())
		if _, err := os.Lstat(filepath.Join(path, "private")); err == nil {
			coverage = append(coverage, coverageEvent(collection, CoveragePrivateRetained, entry.Name()))
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			coverage = append(coverage, coverageEvent(collection, CoverageMetadataUncertain, entry.Name()))
			continue
		}
		switch inspectOwnerMarker(path) {
		case markerMissing:
			coverage = append(coverage, coverageEvent(collection, CoverageForeignEntry, entry.Name()))
			continue
		case markerMalformed:
			coverage = append(coverage, coverageEvent(collection, CoverageMalformedMarker, entry.Name()))
			continue
		}
		if requireCompleted {
			info, err := os.Lstat(filepath.Join(path, "diagnostics.json"))
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				coverage = append(coverage, coverageEvent(collection, CoverageIncompleteEntry, entry.Name()))
				continue
			}
		}
		info, err := entry.Info()
		if err != nil {
			coverage = append(coverage, coverageEvent(collection, CoverageMetadataUncertain, entry.Name()))
			continue
		}
		owned = append(owned, retentionEntry{path: path, modified: info.ModTime()})
	}
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].modified.Equal(owned[j].modified) {
			return owned[i].path < owned[j].path
		}
		return owned[i].modified.After(owned[j].modified)
	})
	for index, entry := range owned {
		expired := maxAge > 0 && now.Sub(entry.modified) > maxAge
		overCount := index >= maxEntries
		if !expired && !overCount {
			continue
		}
		if err := os.RemoveAll(entry.path); err != nil {
			return coverage, err
		}
	}
	return coverage, nil
}

type markerState uint8

const (
	markerOwned markerState = iota
	markerMissing
	markerMalformed
)

func inspectOwnerMarker(directory string) markerState {
	marker := filepath.Join(directory, OwnerMarkerName)
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return markerMissing
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return markerMalformed
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != OwnerMarkerText {
		return markerMalformed
	}
	return markerOwned
}

func coverageEvent(collection CoverageCollection, reason CoverageReason, entryName string) CoverageEvent {
	digest := sha256.Sum256([]byte(entryName))
	return CoverageEvent{
		Collection:  collection,
		Reason:      reason,
		EntrySHA256: hex.EncodeToString(digest[:]),
	}
}
