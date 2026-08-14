package gitcontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/run"
	"github.com/stretchr/testify/require"
)

func TestExplicitUntrackedContentIsFilteredAndBounded(t *testing.T) {
	repository := newTestRepository(t, "untracked")
	repository.write(t, "base.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	repository.write(t, "normal.go", []byte("hello\n"))
	repository.write(t, ".env.local", []byte("DO_NOT_LEAK=env\n"))
	repository.write(t, "binary.dat", []byte{0, 1, 2})
	repository.write(t, "config.txt", []byte("password=DO_NOT_LEAK\n"))
	repository.write(t, "generated/new.pb.go", []byte("GENERATED_SENTINEL\n"))
	repository.write(t, "large.txt", []byte("0123456789ABCDEFGHIJ"))
	require.NoError(t, os.Symlink(filepath.Join(repository.path, "normal.go"), filepath.Join(repository.path, "link.go")))

	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository:              repository.path,
		Source:                  run.SourceWorkingTree,
		IncludeUntrackedContent: true,
		MaxUntrackedBytes:       10,
	})
	require.NoError(t, err)
	if !strings.Contains(snapshot.Patches.Full, "+hello") {
		t.Fatalf("normal untracked content is missing:\n%s", snapshot.Patches.Full)
	}
	for _, test := range []struct {
		name, content string
	}{
		{name: "secret-like content", content: "DO_NOT_LEAK"},
		{name: "generated content", content: "GENERATED_SENTINEL"},
	} {
		if strings.Contains(snapshot.Patches.Full, test.content) {
			t.Errorf("filtered %s leaked into patch", test.name)
		}
	}
	if normal := findUntracked(t, snapshot, "normal.go"); !normal.ContentIncluded || normal.Truncated {
		t.Fatalf("normal untracked metadata = %#v", normal)
	}
	if env := findUntracked(t, snapshot, ".env.local"); env.ContentIncluded || env.ExclusionReason != "restricted" {
		t.Fatalf("env metadata = %#v", env)
	}
	if binary := findUntracked(t, snapshot, "binary.dat"); binary.ContentIncluded || !binary.Binary {
		t.Fatalf("binary metadata = %#v", binary)
	}
	if secret := findUntracked(t, snapshot, "config.txt"); secret.ContentIncluded || secret.ExclusionReason != "secret-like-content" {
		t.Fatalf("secret metadata = %#v", secret)
	}
	if generated := findUntracked(t, snapshot, "generated/new.pb.go"); generated.ContentIncluded || generated.ExclusionReason != "generated" {
		t.Fatalf("generated metadata = %#v", generated)
	}
	if link := findUntracked(t, snapshot, "link.go"); link.ContentIncluded || link.ExclusionReason != "symlink" {
		t.Fatalf("symlink metadata = %#v", link)
	}
	if large := findUntracked(t, snapshot, "large.txt"); !large.ContentIncluded || !large.Truncated {
		t.Fatalf("large metadata = %#v", large)
	}
	if !strings.Contains(snapshot.Patches.Full, "zephyr: untracked content truncated") {
		t.Fatalf("truncation marker is missing:\n%s", snapshot.Patches.Full)
	}
	if !snapshot.HasReviewableChanges() || snapshot.Stats.ReviewableUntracked != 2 {
		t.Fatalf("reviewability/stats = %v/%#v", snapshot.HasReviewableChanges(), snapshot.Stats)
	}
}

func TestLikelySecretDetectionDoesNotExcludeGoAllowlistFixture(t *testing.T) {
	fixture := []byte(`package environment_test

func TestClosedEnvironment(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-parent-secret")
}
`)

	require.False(t, containsLikelySecret(fixture))
	require.True(t, containsLikelySecret([]byte("AWS_SECRET_ACCESS_KEY=real-secret-value\n")))
}
