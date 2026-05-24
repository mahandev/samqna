package pipeline

import (
	"context"
	"os"
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

func TestPruner_RemovesOldUnstarredVideos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	sr := repository.NewSubmissionRepo(db)

	root := t.TempDir()
	st := storage.New(root, root)
	require.NoError(t, st.EnsureRoots())

	// create old + new + starred
	mk := func(age time.Duration, starred bool) *model.Submission {
		id := model.NewSubmissionID()
		ts := time.Now().Add(-age)
		paths := st.PathsFor(id, ts)
		require.NoError(t, st.EnsureDir(paths))
		vp := paths.Original + ".mp4"
		require.NoError(t, os.WriteFile(vp, []byte("x"), 0o644))
		s := &model.Submission{
			ID: id, CreatedAt: ts, SubmitterIP: "x",
			Status: model.StatusReady, VideoPath: &vp,
			AudioPath: paths.Audio, Starred: starred,
		}
		require.NoError(t, sr.Create(s))
		return s
	}
	old := mk(40*24*time.Hour, false)
	young := mk(5*24*time.Hour, false)
	starredOld := mk(40*24*time.Hour, true)

	p := NewPruner(sr, st, 30)
	require.NoError(t, p.RunOnce(context.Background()))

	gotOld, _ := sr.Get(old.ID)
	require.Nil(t, gotOld.VideoPath)
	require.NotNil(t, gotOld.PrunedAt)
	_, err = os.Stat(*old.VideoPath)
	require.True(t, os.IsNotExist(err))

	gotYoung, _ := sr.Get(young.ID)
	require.NotNil(t, gotYoung.VideoPath)
	_, err = os.Stat(filepath.Clean(*young.VideoPath))
	require.NoError(t, err)

	gotStarred, _ := sr.Get(starredOld.ID)
	require.NotNil(t, gotStarred.VideoPath)
}
