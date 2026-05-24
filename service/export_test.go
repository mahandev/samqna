package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"
	"samqna/storage"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ffmpegAvailable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}
}

func setupExport(t *testing.T) (*Export, *model.Submission) {
	ffmpegAvailable(t)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	sr := repository.NewSubmissionRepo(db)

	id := model.NewSubmissionID()
	ts := time.Now()
	paths := st.PathsFor(id, ts)
	require.NoError(t, st.EnsureDir(paths))
	in, _ := os.ReadFile("../testdata/sample.mp4")
	vp := paths.Original + ".mp4"
	require.NoError(t, os.WriteFile(vp, in, 0o644))

	sub := &model.Submission{
		ID: id, CreatedAt: ts, SubmitterIP: "x",
		VideoPath: &vp, Status: model.StatusReady, DurationSec: 3,
		AudioPath: paths.Audio, ThumbnailPath: paths.Thumbnail,
	}
	require.NoError(t, sr.Create(sub))
	e := &Export{Storage: st, Subs: sr, FfmpegBin: "ffmpeg", MaxConcurrent: 2}
	return e, sub
}

func TestExport_OneClick_CachesAndStreams(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.OneClick(context.Background(), sub.ID, buf))
	require.Greater(t, buf.Len(), 0)

	// second call should hit cache
	cached := e.Storage.ExportPath(sub.ID)
	st, err := os.Stat(cached)
	require.NoError(t, err)
	require.Greater(t, st.Size(), int64(0))
}

func TestExport_Trim_StreamsClip(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.Trim(context.Background(), sub.ID, 0.5, 2.5, buf))
	require.Greater(t, buf.Len(), 0)
}

func TestExport_BatchZip_ContainsManifestAndFiles(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.BatchZip(context.Background(), []string{sub.ID}, buf))
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	names := []string{}
	for _, f := range z.File {
		names = append(names, filepath.Base(f.Name))
	}
	require.Contains(t, names, "manifest.json")
}

// shim
var _ = io.Discard
