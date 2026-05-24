package route

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"samqna/config"
	"samqna/migrations"
	"samqna/repository"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	vw, err := view.New()
	require.NoError(t, err)
	deps := &Deps{
		Cfg:     &config.Config{MaxUploadBytes: 1 << 20, MaxIPPerDay: 3, QualityThreshold: 30, AdminToken: "x"},
		DB:      db,
		Subs:    repository.NewSubmissionRepo(db),
		Jobs:    repository.NewJobRepo(db),
		Tags:    repository.NewTagRepo(db),
		IPs:     repository.NewIPRepo(db),
		Storage: st,
		View:    vw,
		Submissions: &service.Submissions{
			Subs: repository.NewSubmissionRepo(db),
			Jobs: repository.NewJobRepo(db),
			Tags: repository.NewTagRepo(db),
			IPs:  repository.NewIPRepo(db),
			Storage: st, MaxBytes: 1 << 20,
		},
	}
	deps.ExportSvc = &service.Export{
		Storage: st, Subs: deps.Subs, FfmpegBin: "ffmpeg", MaxConcurrent: 2,
	}
	RegisterPublic(r, deps)
	return r, db
}

func TestHealthz(t *testing.T) {
	r, _ := newRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/healthz", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "ok", body["status"])
}

func TestSubmitUpload_HappyPath(t *testing.T) {
	r, db := newRouter(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "q.mp4")
	part.Write([]byte("fakebytes"))
	mw.WriteField("consent", "on")
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:5555"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)
	var n int64
	db.Table("submissions").Count(&n)
	require.Equal(t, int64(1), n)
}

func TestSubmitUpload_NoConsent_Rejected(t *testing.T) {
	r, _ := newRouter(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "q.mp4")
	part.Write([]byte("x"))
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestExport_OneClick_Streams(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	r, _ := newRouter(t)
	// upload a real fixture so OneClick has something to remux
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "sample.mp4")
	src, _ := os.ReadFile("../testdata/sample.mp4")
	part.Write(src)
	mw.WriteField("consent", "on")
	mw.Close()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:5555"
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	// extract id from Location header /v/{id}
	loc := w.Header().Get("Location")
	id := strings.TrimPrefix(loc, "/v/")

	// hit one-click export
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v/"+id+"/export", nil)
	r.ServeHTTP(w2, req2)
	require.Equal(t, 200, w2.Code)
	require.Greater(t, w2.Body.Len(), 100)
	require.Equal(t, "video/mp4", w2.Header().Get("Content-Type"))
}
