//go:build integration

package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEndToEnd_SubmitProcessExport(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required for integration test")
	}
	if os.Getenv("WHISPER_BIN") == "" || os.Getenv("WHISPER_MODEL_PATH") == "" {
		t.Skip("WHISPER_BIN / WHISPER_MODEL_PATH required")
	}
	// LLM stub
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[\"test\"],\"quality_score\":80,\"summary\":\"hi\",\"is_spam\":false}"}}]}`))
	}))
	defer llm.Close()

	tmp := t.TempDir()
	t.Setenv("DATABASE_PATH", tmp+"/db")
	t.Setenv("MEDIA_PATH", tmp+"/media")
	t.Setenv("EXPORT_PATH", tmp+"/exports")
	t.Setenv("ADMIN_TOKEN", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("WORKER_COUNT", "1")

	app, err := CreateNewApp()
	require.NoError(t, err)
	app.Pool.Start()
	defer app.Pool.Stop(5 * time.Second)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("consent", "on")
	part, _ := mw.CreateFormFile("video", "sample.mp4")
	src, _ := os.ReadFile("testdata/sample.mp4")
	part.Write(src)
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:0"
	app.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
}
