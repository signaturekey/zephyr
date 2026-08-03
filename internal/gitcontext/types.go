package gitcontext

import (
	"time"

	"github.com/signaturekey/zephyr/internal/run"
)

const SnapshotVersion = 1

type Options struct {
	Repository              string
	Source                  run.Source
	BaseRef                 string
	CommitRange             string
	RestrictedPatterns      []string
	IncludeGenerated        bool
	IncludeVendor           bool
	IncludeUntrackedContent bool
	MaxUntrackedBytes       int64
}

type CommandMetadata struct {
	Args       []string      `json:"args"`
	ExitCode   int           `json:"exit_code"`
	Duration   time.Duration `json:"duration"`
	Successful bool          `json:"successful"`
}

type RepositoryMetadata struct {
	Root         string            `json:"root"`
	GitVersion   string            `json:"git_version"`
	Branch       string            `json:"branch,omitempty"`
	Detached     bool              `json:"detached"`
	HeadSHA      string            `json:"head_sha,omitempty"`
	BaseRef      string            `json:"base_ref,omitempty"`
	BaseSHA      string            `json:"base_sha,omitempty"`
	TargetSHA    string            `json:"target_sha,omitempty"`
	MergeBaseSHA string            `json:"merge_base_sha,omitempty"`
	CommitRange  string            `json:"commit_range,omitempty"`
	Commands     []CommandMetadata `json:"commands"`
}

type StatusEntry struct {
	Path           string `json:"path"`
	PreviousPath   string `json:"previous_path,omitempty"`
	IndexStatus    string `json:"index_status,omitempty"`
	WorktreeStatus string `json:"worktree_status,omitempty"`
	SubmoduleState string `json:"submodule_state,omitempty"`
	Unmerged       bool   `json:"unmerged,omitempty"`
	Untracked      bool   `json:"untracked,omitempty"`
	Ignored        bool   `json:"ignored,omitempty"`
	Generated      bool   `json:"generated,omitempty"`
	Vendor         bool   `json:"vendor,omitempty"`
	Restricted     bool   `json:"restricted,omitempty"`
}

type UntrackedFile struct {
	Path            string `json:"path"`
	Generated       bool   `json:"generated,omitempty"`
	Vendor          bool   `json:"vendor,omitempty"`
	Restricted      bool   `json:"restricted,omitempty"`
	Binary          bool   `json:"binary,omitempty"`
	ContentIncluded bool   `json:"content_included"`
	Truncated       bool   `json:"truncated,omitempty"`
	ExclusionReason string `json:"exclusion_reason,omitempty"`
}

type WorktreeStatus struct {
	Entries   []StatusEntry   `json:"entries"`
	Untracked []UntrackedFile `json:"untracked"`
}

type ChangeStatus string

const (
	ChangeAdded       ChangeStatus = "added"
	ChangeModified    ChangeStatus = "modified"
	ChangeDeleted     ChangeStatus = "deleted"
	ChangeRenamed     ChangeStatus = "renamed"
	ChangeCopied      ChangeStatus = "copied"
	ChangeTypeChanged ChangeStatus = "type-changed"
	ChangeUnmerged    ChangeStatus = "unmerged"
	ChangeUnknown     ChangeStatus = "unknown"
)

type FileChange struct {
	Path            string       `json:"path"`
	PreviousPath    string       `json:"previous_path,omitempty"`
	Status          ChangeStatus `json:"status"`
	GitStatus       string       `json:"git_status"`
	Similarity      int          `json:"similarity,omitempty"`
	OldMode         string       `json:"old_mode,omitempty"`
	NewMode         string       `json:"new_mode,omitempty"`
	Insertions      *int         `json:"insertions,omitempty"`
	Deletions       *int         `json:"deletions,omitempty"`
	Binary          bool         `json:"binary,omitempty"`
	Generated       bool         `json:"generated,omitempty"`
	Vendor          bool         `json:"vendor,omitempty"`
	Restricted      bool         `json:"restricted,omitempty"`
	Submodule       bool         `json:"submodule,omitempty"`
	ContentIncluded bool         `json:"content_included"`
	ExclusionReason string       `json:"exclusion_reason,omitempty"`
}

type DiffStats struct {
	Files               int `json:"files"`
	Insertions          int `json:"insertions"`
	Deletions           int `json:"deletions"`
	Binary              int `json:"binary"`
	Generated           int `json:"generated"`
	Vendor              int `json:"vendor"`
	Restricted          int `json:"restricted"`
	Submodules          int `json:"submodules"`
	Untracked           int `json:"untracked"`
	ReviewableUntracked int `json:"reviewable_untracked"`
}

type Patches struct {
	Full     string `json:"full"`
	Staged   string `json:"staged"`
	Unstaged string `json:"unstaged"`
}

type Snapshot struct {
	Version                 int                `json:"version"`
	CollectedAt             time.Time          `json:"collected_at"`
	Source                  run.Source         `json:"source"`
	Repository              RepositoryMetadata `json:"repository"`
	Status                  WorktreeStatus     `json:"status"`
	Changes                 []FileChange       `json:"changes"`
	Stats                   DiffStats          `json:"stats"`
	Patches                 Patches            `json:"patches"`
	SourceFingerprint       string             `json:"source_fingerprint"`
	WorkingTreeFingerprint  string             `json:"working_tree_fingerprint"`
	IncludeGenerated        bool               `json:"include_generated"`
	IncludeVendor           bool               `json:"include_vendor"`
	IncludeUntrackedContent bool               `json:"include_untracked_content"`
	MaxUntrackedBytes       int64              `json:"max_untracked_bytes,omitempty"`
	RestrictedPatterns      []string           `json:"restricted_patterns,omitempty"`
	Limitations             []string           `json:"limitations,omitempty"`
}

func (s Snapshot) HasChanges() bool {
	return len(s.Changes) > 0 || len(s.Status.Untracked) > 0
}

func (s Snapshot) HasReviewableChanges() bool {
	for _, change := range s.Changes {
		if change.ContentIncluded {
			return true
		}
	}
	for _, untracked := range s.Status.Untracked {
		if untracked.ContentIncluded {
			return true
		}
	}
	return false
}

func (s Snapshot) Options() Options {
	return Options{
		Repository:              s.Repository.Root,
		Source:                  s.Source,
		BaseRef:                 s.Repository.BaseRef,
		CommitRange:             s.Repository.CommitRange,
		RestrictedPatterns:      append([]string(nil), s.RestrictedPatterns...),
		IncludeGenerated:        s.IncludeGenerated,
		IncludeVendor:           s.IncludeVendor,
		IncludeUntrackedContent: s.IncludeUntrackedContent,
		MaxUntrackedBytes:       s.MaxUntrackedBytes,
	}
}

type MetadataArtifact struct {
	Version                 int                `json:"version"`
	CollectedAt             time.Time          `json:"collected_at"`
	Source                  run.Source         `json:"source"`
	Repository              RepositoryMetadata `json:"repository"`
	Stats                   DiffStats          `json:"stats"`
	SourceFingerprint       string             `json:"source_fingerprint"`
	WorkingTreeFingerprint  string             `json:"working_tree_fingerprint"`
	IncludeGenerated        bool               `json:"include_generated"`
	IncludeVendor           bool               `json:"include_vendor"`
	IncludeUntrackedContent bool               `json:"include_untracked_content"`
	MaxUntrackedBytes       int64              `json:"max_untracked_bytes,omitempty"`
	Limitations             []string           `json:"limitations,omitempty"`
}

func (s Snapshot) MetadataArtifact() MetadataArtifact {
	return MetadataArtifact{
		Version:                 s.Version,
		CollectedAt:             s.CollectedAt,
		Source:                  s.Source,
		Repository:              s.Repository,
		Stats:                   s.Stats,
		SourceFingerprint:       s.SourceFingerprint,
		WorkingTreeFingerprint:  s.WorkingTreeFingerprint,
		IncludeGenerated:        s.IncludeGenerated,
		IncludeVendor:           s.IncludeVendor,
		IncludeUntrackedContent: s.IncludeUntrackedContent,
		MaxUntrackedBytes:       s.MaxUntrackedBytes,
		Limitations:             append([]string(nil), s.Limitations...),
	}
}

type StatusArtifact struct {
	Status  WorktreeStatus `json:"status"`
	Changes []FileChange   `json:"changes"`
}

func (s Snapshot) StatusArtifact() StatusArtifact {
	return StatusArtifact{
		Status:  s.Status,
		Changes: append([]FileChange(nil), s.Changes...),
	}
}

type Staleness struct {
	Stale              bool `json:"stale"`
	HeadChanged        bool `json:"head_changed"`
	BaseChanged        bool `json:"base_changed"`
	SourceChanged      bool `json:"source_changed"`
	WorkingTreeChanged bool `json:"working_tree_changed"`
}
