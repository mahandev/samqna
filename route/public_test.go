package route

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// makeUpload builds a small multipart body with consent. Used by several tests.
func makeUpload(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "q.mp4")
	part.Write([]byte("fakebytes"))
	mw.WriteField("consent", "on")
	require.NoError(t, mw.Close())
	return body, mw.FormDataContentType()
}

func TestSubmitUpload_RedirectsToFeedWithNew(t *testing.T) {
	r, db := newRouter(t)
	body, ct := makeUpload(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", ct)
	req.RemoteAddr = "1.2.3.4:5555"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)
	loc := w.Header().Get("Location")
	require.True(t, strings.HasPrefix(loc, "/?new="), "want /?new=... got %q", loc)

	var n int64
	db.Table("submissions").Count(&n)
	require.Equal(t, int64(1), n)
}

func TestSubmit_JSON_AcceptReturnsJSON(t *testing.T) {
	r, _ := newRouter(t)
	body, ct := makeUpload(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = "1.2.3.4:5555"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body2 struct{ ID, Redirect string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body2))
	require.NotEmpty(t, body2.ID)
	require.Equal(t, "/?new="+body2.ID, body2.Redirect)
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

func TestFeedRendersAtRoot(t *testing.T) {
	r, _ := newRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "Ask Sam")                       // navbar CTA — proof layout rendered
	require.Contains(t, body, `data-theme="sulek"`)            // custom DaisyUI theme attribute
	require.Contains(t, body, `id="list"`)                     // feed list container exists
	require.Contains(t, body, "SAM PICKS")                     // hero copy
}

func TestBrowseRedirectsToRoot(t *testing.T) {
	r, _ := newRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/browse", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusMovedPermanently, w.Code)
	require.Equal(t, "/", w.Header().Get("Location"))
}

func TestExport_OneClick_Streams(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	r, _ := newRouter(t)
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

	// Location is /?new=ULID — pull the ULID out of the query string.
	u, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	id := u.Query().Get("new")
	require.NotEmpty(t, id)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v/"+id+"/export", nil)
	r.ServeHTTP(w2, req2)
	require.Equal(t, 200, w2.Code)
	require.Greater(t, w2.Body.Len(), 100)
	require.Equal(t, "video/mp4", w2.Header().Get("Content-Type"))
}
