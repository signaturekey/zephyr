package gitcontext

import (
	"context"
	"fmt"
)

func (c *Collector) CheckStale(ctx context.Context, original Snapshot) (Staleness, error) {
	if original.Version != SnapshotVersion {
		return Staleness{}, fmt.Errorf("check snapshot staleness: unsupported snapshot version %d", original.Version)
	}
	current, err := c.Collect(ctx, original.Options())
	if err != nil {
		return Staleness{}, fmt.Errorf("check snapshot staleness: %w", err)
	}
	result := Staleness{
		HeadChanged: original.Repository.HeadSHA != current.Repository.HeadSHA,
		BaseChanged: original.Repository.BaseSHA != current.Repository.BaseSHA ||
			original.Repository.TargetSHA != current.Repository.TargetSHA ||
			original.Repository.MergeBaseSHA != current.Repository.MergeBaseSHA,
		SourceChanged:      original.SourceFingerprint != current.SourceFingerprint,
		WorkingTreeChanged: original.WorkingTreeFingerprint != current.WorkingTreeFingerprint,
	}
	result.Stale = result.HeadChanged || result.BaseChanged || result.SourceChanged || result.WorkingTreeChanged
	return result, nil
}
