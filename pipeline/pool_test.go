package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	return db
}

type countingStage struct {
	name string
	next string
	hits *int32
	fail bool
}

func (s *countingStage) Name() string { return s.name }
func (s *countingStage) Next() string { return s.next }
func (s *countingStage) Run(_ context.Context, _ *model.Submission) error {
	atomic.AddInt32(s.hits, 1)
	if s.fail {
		return errors.New("forced fail")
	}
	return nil
}

func TestPool_ProcessesJobThroughAllStages(t *testing.T) {
	db := newDB(t)
	sr := repository.NewSubmissionRepo(db)
	jr := repository.NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	var c1, c2, c3 int32
	reg := NewRegistry()
	reg.Register(&countingStage{name: "extract", next: "transcribe", hits: &c1})
	reg.Register(&countingStage{name: "transcribe", next: "tag_grade", hits: &c2})
	reg.Register(&countingStage{name: "tag_grade", next: "", hits: &c3})

	pool := NewPool(db, sr, jr, reg, 1, 30*time.Millisecond, 5)
	pool.Start()
	defer pool.Stop(2 * time.Second)

	require.Eventually(t, func() bool {
		got, _ := sr.Get(s.ID)
		return got.Status == model.StatusReady
	}, 3*time.Second, 50*time.Millisecond)

	require.EqualValues(t, 1, atomic.LoadInt32(&c1))
	require.EqualValues(t, 1, atomic.LoadInt32(&c2))
	require.EqualValues(t, 1, atomic.LoadInt32(&c3))
}

func TestPool_FailureRetriesThenPermanentFailure(t *testing.T) {
	db := newDB(t)
	sr := repository.NewSubmissionRepo(db)
	jr := repository.NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	var hits int32
	reg := NewRegistry()
	reg.Register(&countingStage{name: "extract", next: "transcribe", hits: &hits, fail: true})

	// maxAttempts=2 to keep test fast; pool uses immediate backoff override via 0
	pool := NewPool(db, sr, jr, reg, 1, 30*time.Millisecond, 2)
	pool.backoffOverride = 10 * time.Millisecond
	pool.Start()
	defer pool.Stop(2 * time.Second)

	require.Eventually(t, func() bool {
		got, _ := sr.Get(s.ID)
		return got.Status == model.StatusFailed
	}, 3*time.Second, 50*time.Millisecond)

	require.EqualValues(t, 2, atomic.LoadInt32(&hits))
}
