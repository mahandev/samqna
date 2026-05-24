package repository

import (
	"testing"
	"time"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

func TestJobs_EnqueueAndClaim(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, err := jr.Claim("worker-1")
	require.NoError(t, err)
	require.NotNil(t, j)
	require.Equal(t, s.ID, j.SubmissionID)
	require.Equal(t, model.JobRunning, j.Status)
	require.NotNil(t, j.LockedBy)
}

func TestJobs_Claim_ReturnsNilWhenEmpty(t *testing.T) {
	db := newTestDB(t)
	jr := NewJobRepo(db)
	j, err := jr.Claim("w")
	require.NoError(t, err)
	require.Nil(t, j)
}

func TestJobs_ReleaseStaleLocks(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("worker-1")
	// fake stale lock by backdating
	old := time.Now().Add(-15 * time.Minute)
	db.Model(j).Update("locked_at", old)

	n, err := jr.ReleaseStaleLocks(10 * time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestJobs_AdvanceStage(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("w")
	require.NoError(t, jr.AdvanceStage(j.ID, model.StageTranscribe))

	got, err := jr.GetBySubmission(s.ID)
	require.NoError(t, err)
	require.Equal(t, model.StageTranscribe, got.Stage)
	require.Equal(t, model.JobPending, got.Status)
}

func TestJobs_RecordFailure_SchedulesBackoff(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("w")
	require.NoError(t, jr.RecordFailure(j.ID, "boom"))

	got, _ := jr.GetBySubmission(s.ID)
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, model.JobPending, got.Status)
	require.True(t, got.NextRunAt.After(time.Now()))
}

func TestCanonicalize(t *testing.T) {
	in := []string{"AI", "First Job", "AI", "  career  ", "C++!"}
	got := Canonicalize(in)
	want := []string{"ai", "first-job", "career", "c"}
	require.Equal(t, want, got)
}

func TestJobs_RecordFailureWithBackoff(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("w")
	require.NoError(t, jr.RecordFailureWithBackoff(j.ID, "boom", 10*time.Millisecond))

	got, _ := jr.GetBySubmission(s.ID)
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, model.JobPending, got.Status)
	require.WithinDuration(t, time.Now().Add(10*time.Millisecond), got.NextRunAt, 200*time.Millisecond)
}
