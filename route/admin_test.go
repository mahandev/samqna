package route

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"samqna/config"
	"samqna/model"
	"samqna/repository"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdmin_Star_RequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := newRouter(t)
	// register admin (manual because newRouter only does public)
	depsAdmin := &Deps{
		Cfg: &config.Config{AdminToken: "topsecret"},
		Subs: repository.NewSubmissionRepo(db), Jobs: repository.NewJobRepo(db),
		IPs: repository.NewIPRepo(db),
	}
	RegisterAdmin(r, depsAdmin)

	sub := &model.Submission{ID: model.NewSubmissionID(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusReady}
	require.NoError(t, depsAdmin.Subs.Create(sub))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code) // hidden without token

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	req.Header.Set("X-Admin-Token", "topsecret")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	got, _ := depsAdmin.Subs.Get(sub.ID)
	require.True(t, got.Starred)
}
