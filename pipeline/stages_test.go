package pipeline

import (
	"context"
	"errors"
	"testing"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

type fakeStage struct {
	name    string
	next    string
	runErr  error
	called  bool
}

func (f *fakeStage) Name() string { return f.name }
func (f *fakeStage) Next() string { return f.next }
func (f *fakeStage) Run(_ context.Context, _ *model.Submission) error {
	f.called = true
	return f.runErr
}

func TestRegistry_Lookup(t *testing.T) {
	r := NewRegistry()
	a := &fakeStage{name: "extract", next: "transcribe"}
	r.Register(a)
	got, ok := r.Get("extract")
	require.True(t, ok)
	require.Equal(t, a, got)
}

func TestRunStage_Success_AdvancesToNext(t *testing.T) {
	s := &fakeStage{name: "extract", next: "transcribe"}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.True(t, s.called)
	require.NoError(t, res.Err)
	require.Equal(t, "transcribe", res.NextStage)
	require.False(t, res.Terminal)
}

func TestRunStage_Failure_PreservesStage(t *testing.T) {
	s := &fakeStage{name: "extract", next: "transcribe", runErr: errors.New("boom")}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.Error(t, res.Err)
	require.Equal(t, "", res.NextStage)
}

func TestRunStage_TerminalStage(t *testing.T) {
	s := &fakeStage{name: "tag_grade", next: ""}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.NoError(t, res.Err)
	require.True(t, res.Terminal)
}
