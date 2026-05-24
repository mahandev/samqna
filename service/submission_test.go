package service

import (
	"bytes"
	"io"
	"strings"
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

func setup(t *testing.T) (*Submissions, *gorm.DB) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	svc := &Submissions{
		Subs:    repository.NewSubmissionRepo(db),
		Jobs:    repository.NewJobRepo(db),
		Tags:    repository.NewTagRepo(db),
		Storage: st,
		MaxBytes: 1 << 20,
	}
	return svc, db
}

func TestSubmissions_AcceptUpload_CreatesSubmissionAndJob(t *testing.T) {
	svc, db := setup(t)
	body := bytes.NewReader([]byte("fakevideobytes"))
	res, err := svc.AcceptUpload(AcceptInput{
		IP: "1.2.3.4", OriginalFilename: "q.mp4",
		Reader: body, Size: int64(body.Len()),
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.ID)

	got, err := svc.Subs.Get(res.ID)
	require.NoError(t, err)
	require.Equal(t, model.StatusProcessing, got.Status)
	require.NotNil(t, got.VideoPath)

	var n int64
	db.Model(&model.Job{}).Where("submission_id = ?", res.ID).Count(&n)
	require.Equal(t, int64(1), n)
}

func TestSubmissions_AcceptUpload_RejectsOversize(t *testing.T) {
	svc, _ := setup(t)
	svc.MaxBytes = 5
	_, err := svc.AcceptUpload(AcceptInput{
		IP: "x", OriginalFilename: "q.mp4",
		Reader: strings.NewReader("toolong"), Size: 7,
	})
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestSubmissions_AcceptUpload_StreamReaderTooLong(t *testing.T) {
	svc, _ := setup(t)
	svc.MaxBytes = 5
	// size unknown (0), but reader gives more than MaxBytes
	rdr := io.NopCloser(strings.NewReader("toolong"))
	_, err := svc.AcceptUpload(AcceptInput{
		IP: "x", OriginalFilename: "q.mp4",
		Reader: rdr, Size: 0,
	})
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestSubmissions_RateLimit(t *testing.T) {
	svc, _ := setup(t)
	for i := 0; i < 3; i++ {
		_, err := svc.AcceptUpload(AcceptInput{IP: "9.9.9.9", OriginalFilename: "q.mp4", Reader: strings.NewReader("x"), Size: 1})
		require.NoError(t, err)
	}
	err := svc.CheckRateLimit("9.9.9.9", 3, 24*time.Hour)
	require.ErrorIs(t, err, ErrRateLimit)
}
