package route

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"samqna/config"
	"samqna/model"
	"samqna/repository"
	"samqna/service"
	"samqna/view"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdmin_Star_RequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := newRouter(t)

	vw, err := view.New()
	require.NoError(t, err)

	depsAdmin := &Deps{
		Cfg:  &config.Config{AdminToken: "topsecret"},
		DB:   db,
		Subs: repository.NewSubmissionRepo(db),
		Jobs: repository.NewJobRepo(db),
		Tags: repository.NewTagRepo(db),
		IPs:  repository.NewIPRepo(db),
		View: vw,
	}
	depsAdmin.AdminSvc = &service.Admin{
		DB:       db,
		Subs:     depsAdmin.Subs,
		Jobs:     depsAdmin.Jobs,
		Tags:     depsAdmin.Tags,
		IPs:      depsAdmin.IPs,
		Audits:   repository.NewAuditRepo(db),
		Settings: repository.NewSettingsRepo(db),
	}
	RegisterAdmin(r, depsAdmin)

	sub := &model.Submission{ID: model.NewSubmissionID(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusReady}
	require.NoError(t, depsAdmin.Subs.Create(sub))

	// 1) Without token → 404 (hidden).
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)

	// 2) Token but no X-Confirm header → 428.
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	req.Header.Set("X-Admin-Token", "topsecret")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusPreconditionRequired, w.Code)

	// 3) Token + X-Confirm → 200, star flipped, audit row written.
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	req.Header.Set("X-Admin-Token", "topsecret")
	req.Header.Set("X-Confirm", "yes")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	got, _ := depsAdmin.Subs.Get(sub.ID)
	require.True(t, got.Starred)

	audits, _ := depsAdmin.AdminSvc.RecentAudits(10)
	require.NotEmpty(t, audits)
	require.Equal(t, "token", audits[0].Actor)
	require.Equal(t, "star_on", audits[0].Action)
	require.Equal(t, sub.ID, audits[0].Target)
}

func TestAdmin_Pause_BlocksSubmit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := newRouter(t)
	vw, err := view.New()
	require.NoError(t, err)

	deps := &Deps{
		Cfg:  &config.Config{AdminToken: "topsecret", MaxUploadBytes: 1 << 20, MaxIPPerDay: 3},
		DB:   db,
		Subs: repository.NewSubmissionRepo(db),
		Jobs: repository.NewJobRepo(db),
		Tags: repository.NewTagRepo(db),
		IPs:  repository.NewIPRepo(db),
		View: vw,
	}
	deps.AdminSvc = &service.Admin{
		DB:       db,
		Subs:     deps.Subs,
		Jobs:     deps.Jobs,
		Tags:     deps.Tags,
		IPs:      deps.IPs,
		Audits:   repository.NewAuditRepo(db),
		Settings: repository.NewSettingsRepo(db),
	}
	require.NoError(t, deps.AdminSvc.Pause("test"))
	require.True(t, deps.AdminSvc.IsPaused())

	// Re-register submit so it sees the new deps with AdminSvc paused.
	// newRouter already registered public routes against a different Submissions
	// service, so this is a separate engine for this test:
	r2 := gin.New()
	// Need full submit dependency:
	deps.Submissions = &service.Submissions{
		Subs: deps.Subs, Jobs: deps.Jobs, Tags: deps.Tags, IPs: deps.IPs,
		Storage: nil, MaxBytes: 1 << 20,
	}
	// Storage isn't reachable in this test but pause check fires first so we never get there.
	RegisterPublic(r2, deps)

	body, ct := makeUpload(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", ct)
	req.RemoteAddr = "1.2.3.4:0"
	r2.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	// Avoid the unused 'r' lint:
	_ = r
}
