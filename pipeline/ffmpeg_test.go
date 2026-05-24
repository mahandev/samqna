package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"samqna/model"
	"samqna/storage"

	"github.com/stretchr/testify/require"
)

func ffmpegAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}
}

func TestExtractStage_ProducesAudioAndThumb(t *testing.T) {
	ffmpegAvailable(t)
	root := t.TempDir()
	st := storage.New(root, root)

	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := st.PathsFor(id, createdAt)
	require.NoError(t, st.EnsureDir(paths))

	// copy fixture to original path
	in, err := os.ReadFile("../testdata/sample.mp4")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Original+".mp4", in, 0o644))

	sub := &model.Submission{
		ID: id, CreatedAt: createdAt, Status: model.StatusProcessing,
		VideoPath: ptr(paths.Original + ".mp4"),
	}
	s := &ExtractStage{Storage: st, FfmpegBin: "ffmpeg"}
	require.NoError(t, s.Run(context.Background(), sub))

	st1, err := os.Stat(paths.Audio)
	require.NoError(t, err)
	require.Greater(t, st1.Size(), int64(0))
	st2, err := os.Stat(paths.Thumbnail)
	require.NoError(t, err)
	require.Greater(t, st2.Size(), int64(0))
	require.Equal(t, paths.Audio, sub.AudioPath)
	require.Equal(t, paths.Thumbnail, sub.ThumbnailPath)
	require.Greater(t, sub.DurationSec, 0)
	require.Equal(t, "transcribe", s.Next())

	// sanity that paths exist
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { return nil })
}

func ptr[T any](v T) *T { return &v }
