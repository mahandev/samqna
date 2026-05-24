package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

func TestTagGradeStage_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "transcribed text")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"tags\":[\"career\",\"AI\"],\"quality_score\":78,\"summary\":\"asking about AI jobs\",\"is_spam\":false,\"spam_reason\":null}"}}]
		}`))
	}))
	defer server.Close()

	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client:   server.Client(),
		Endpoint: server.URL,
		APIKey:   "test",
		Models:   []string{"google/gemini-2.5-flash"},
		QualityThreshold: 30,
		TagRepo:  tr,
		AttachTags: func(_ *model.Submission, tags []model.Tag) error {
			require.Len(t, tags, 2)
			return nil
		},
	}
	tx := "transcribed text"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, model.StatusProcessing, sub.Status) // ready set by pool, not stage
	require.NotNil(t, sub.QualityScore)
	require.Equal(t, 78, *sub.QualityScore)
	require.Equal(t, "asking about AI jobs", *sub.Summary)
}

func TestTagGradeStage_LowScoreQuarantines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[],\"quality_score\":10,\"summary\":\"unclear\",\"is_spam\":false}"}}]}`))
	}))
	defer server.Close()
	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client: server.Client(), Endpoint: server.URL, APIKey: "x",
		Models: []string{"m"}, QualityThreshold: 30, TagRepo: tr,
		AttachTags: func(_ *model.Submission, _ []model.Tag) error { return nil },
	}
	tx := "x"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, model.StatusQuarantined, sub.Status)
}

func TestTagGradeStage_FallsBackToSecondModel(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		body, _ := io.ReadAll(r.Body)
		if hits == 1 {
			require.True(t, strings.Contains(string(body), "model-a"))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		require.True(t, strings.Contains(string(body), "model-b"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[\"t\"],\"quality_score\":80,\"summary\":\"x\",\"is_spam\":false}"}}]}`))
	}))
	defer server.Close()
	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client: server.Client(), Endpoint: server.URL, APIKey: "x",
		Models: []string{"model-a", "model-b"}, QualityThreshold: 30, TagRepo: tr,
		AttachTags: func(_ *model.Submission, _ []model.Tag) error { return nil },
	}
	tx := "x"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, 2, hits)
}

type fakeTagRepo struct{}

func (f *fakeTagRepo) GetOrCreate(names []string) ([]model.Tag, error) {
	out := make([]model.Tag, 0, len(names))
	for i, n := range names {
		out = append(out, model.Tag{ID: uint(i + 1), Name: n})
	}
	return out, nil
}

// sanity: discard
var _ = json.RawMessage("{}")
