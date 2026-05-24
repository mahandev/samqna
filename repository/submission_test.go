package repository

import (
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	return db
}

func TestSubmissions_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)

	s := &model.Submission{
		ID:               model.NewSubmissionID(),
		CreatedAt:        time.Now(),
		SubmitterIP:      "1.2.3.4",
		OriginalFilename: "q.mp4",
		AudioPath:        "/tmp/a.opus",
		ThumbnailPath:    "/tmp/t.jpg",
		Status:           model.StatusProcessing,
	}
	require.NoError(t, r.Create(s))

	got, err := r.Get(s.ID)
	require.NoError(t, err)
	require.Equal(t, s.SubmitterIP, got.SubmitterIP)
}

func TestSubmissions_ListReady_FiltersAndPaginates(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)
	for i := 0; i < 5; i++ {
		s := &model.Submission{
			ID:          model.NewSubmissionID(),
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Minute),
			SubmitterIP: "1.2.3.4",
			AudioPath:   "/x",
			Status:      model.StatusReady,
		}
		score := 50 + i
		s.QualityScore = &score
		require.NoError(t, r.Create(s))
	}
	out, err := r.ListReady(ListFilter{MinScore: 52, Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, out, 3) // scores 52, 53, 54
}

func TestSubmissions_CountFromIPSince(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)
	now := time.Now()
	for i := 0; i < 3; i++ {
		s := &model.Submission{
			ID:          model.NewSubmissionID(),
			CreatedAt:   now,
			SubmitterIP: "9.9.9.9",
			AudioPath:   "/x",
			Status:      model.StatusReady,
		}
		require.NoError(t, r.Create(s))
	}
	n, err := r.CountFromIPSince("9.9.9.9", now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(3), n)
}
