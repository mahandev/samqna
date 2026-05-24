package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"samqna/auth"
	"samqna/config"
	"samqna/model"
	"samqna/repository"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Deps holds all dependencies injected into the route layer.
type Deps struct {
	Cfg         *config.Config
	DB          *gorm.DB
	Subs        *repository.SubmissionRepo
	Jobs        *repository.JobRepo
	Tags        *repository.TagRepo
	IPs         *repository.IPRepo
	Storage     *storage.Storage
	View        *view.Renderer
	Submissions *service.Submissions
	ExportSvc   *service.Export
	AdminSvc    *service.Admin
	CFAccess    *auth.Verifier
}

// RegisterPublic mounts all unauthenticated routes onto r.
func RegisterPublic(r *gin.Engine, d *Deps) {
	r.StaticFS("/static", http.Dir("static"))

	// Feed is the home page.
	r.GET("/", func(c *gin.Context) { feedHandler(c, d, false) })
	// Legacy /browse redirects to root so saved links still work.
	r.GET("/browse", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/") })
	// HTMX fragment endpoint used by the filter form for partial swaps.
	r.GET("/browse/list", func(c *gin.Context) { feedHandler(c, d, true) })

	r.GET("/submit", func(c *gin.Context) {
		render(c, d.View, "submit", gin.H{"TurnstileSite": d.Cfg.TurnstileSite})
	})
	r.POST("/submit", func(c *gin.Context) { submitHandler(c, d) })

	r.GET("/v/:id", func(c *gin.Context) { videoHandler(c, d) })
	r.GET("/v/:id/status", func(c *gin.Context) { statusHandler(c, d) })
	r.GET("/v/:id/live", func(c *gin.Context) { liveHandler(c, d) })
	r.GET("/v/:id/card", func(c *gin.Context) { cardHandler(c, d) })
	r.GET("/v/:id/thumb", func(c *gin.Context) { fileHandler(c, d, "thumb") })
	r.GET("/v/:id/video", func(c *gin.Context) { fileHandler(c, d, "video") })
	r.GET("/v/:id/audio", func(c *gin.Context) { fileHandler(c, d, "audio") })

	r.GET("/tags", func(c *gin.Context) {
		m, err := d.Tags.AllWithCounts()
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, m)
	})
	r.GET("/healthz", func(c *gin.Context) { healthHandler(c, d) })

	r.GET("/v/:id/export", func(c *gin.Context) {
		c.Header("Content-Type", "video/mp4")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="clip-%s.mp4"`, c.Param("id")))
		if err := d.ExportSvc.OneClick(c.Request.Context(), c.Param("id"), c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
	r.POST("/v/:id/export/trim", func(c *gin.Context) {
		var body struct{ Start, End float64 }
		if err := c.ShouldBindJSON(&body); err != nil {
			c.AbortWithStatus(400)
			return
		}
		c.Header("Content-Type", "video/mp4")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="clip-%s.mp4"`, c.Param("id")))
		if err := d.ExportSvc.Trim(c.Request.Context(), c.Param("id"), body.Start, body.End, c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
	r.POST("/export/batch", func(c *gin.Context) {
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
			c.AbortWithStatus(400)
			return
		}
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", `attachment; filename="samqna-batch.zip"`)
		if err := d.ExportSvc.BatchZip(c.Request.Context(), body.IDs, c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
}

func render(c *gin.Context, vw *view.Renderer, name string, data any) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := vw.Render(c.Writer, name, data); err != nil {
		_ = c.AbortWithError(500, err)
	}
}

// wantsJSON returns true when the client prefers JSON (used to switch
// submitHandler between redirect and JSON-with-redirect-target).
func wantsJSON(c *gin.Context) bool {
	a := c.GetHeader("Accept")
	return strings.Contains(a, "application/json")
}

// isAdmin checks whether the current request carries a valid Cloudflare
// Access JWT (i.e. the user is signed in as the creator). The public
// pages stay readable to everyone — admin status only controls whether
// the inline manage buttons render.
func isAdmin(c *gin.Context, d *Deps) bool {
	if d.CFAccess == nil || !d.CFAccess.Enabled() {
		return false
	}
	raw := c.GetHeader("Cf-Access-Jwt-Assertion")
	_, ok := d.CFAccess.ValidEmail(raw)
	return ok
}

func submitHandler(c *gin.Context, d *Deps) {
	// Global kill switch: admin paused submissions.
	if d.AdminSvc != nil && d.AdminSvc.IsPaused() {
		c.String(http.StatusServiceUnavailable, "Submissions temporarily paused. Try again later.")
		return
	}
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.String(http.StatusBadRequest, "Could not parse upload.")
		return
	}
	if c.Request.FormValue("consent") == "" {
		c.String(http.StatusBadRequest, "Consent required.")
		return
	}
	ip := clientIP(c)

	if d.IPs != nil {
		if blocked, err := d.IPs.IsBlocked(ip); err == nil && blocked {
			c.String(http.StatusForbidden, "Blocked.")
			return
		}
	}
	if err := d.Submissions.CheckRateLimit(ip, d.Cfg.MaxIPPerDay, 24*time.Hour); errors.Is(err, service.ErrRateLimit) {
		c.String(http.StatusTooManyRequests, "Daily submission limit reached.")
		return
	}
	if d.Cfg.TurnstileSecret != "" {
		if !verifyTurnstile(d.Cfg.TurnstileSecret, c.Request.FormValue("cf-turnstile-response"), ip) {
			c.String(http.StatusBadRequest, "Bot check failed.")
			return
		}
	}
	file, header, err := c.Request.FormFile("video")
	if err != nil {
		c.String(http.StatusBadRequest, "No video provided.")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".mp4" && ext != ".webm" && ext != ".mov" {
		ext = ".mp4"
	}
	res, err := d.Submissions.AcceptUpload(service.AcceptInput{
		IP:               ip,
		Email:            c.Request.FormValue("email"),
		OriginalFilename: header.Filename,
		Reader:           file,
		Size:             header.Size,
		Ext:              ext,
	})
	switch {
	case errors.Is(err, service.ErrTooLarge):
		c.String(http.StatusRequestEntityTooLarge, "Video too large (max 50 MB).")
		return
	case err != nil:
		c.String(http.StatusInternalServerError, "Server error.")
		return
	}

	redirect := "/?new=" + res.ID
	if wantsJSON(c) {
		c.JSON(http.StatusOK, gin.H{"id": res.ID, "redirect": redirect})
		return
	}
	c.Redirect(http.StatusSeeOther, redirect)
}

// View structs shared between feed and single-card endpoints — keeps GORM
// models out of templates and gives us a stable shape for HTMX swaps.
type tagView struct{ Name string }

type subView struct {
	ID           string
	Summary      string
	Transcript   string
	QualityScore int
	Status       string
	Tags         []tagView
	IsNew        bool
	IsAdmin      bool
}

func flatten(s *model.Submission, newID string) subView {
	tv := make([]tagView, 0, len(s.Tags))
	for _, t := range s.Tags {
		tv = append(tv, tagView{Name: t.Name})
	}
	score := 0
	if s.QualityScore != nil {
		score = *s.QualityScore
	}
	return subView{
		ID:           s.ID,
		Summary:      deref(s.Summary),
		Transcript:   deref(s.Transcript),
		QualityScore: score,
		Status:       string(s.Status),
		Tags:         tv,
		IsNew:        s.ID == newID && newID != "",
	}
}

func feedHandler(c *gin.Context, d *Deps, fragment bool) {
	tags := c.QueryArray("tag")
	minScore, _ := strconv.Atoi(c.DefaultQuery("min_score", "0"))
	starred := c.Query("starred") == "1"
	newID := c.Query("new")
	admin := isAdmin(c, d)
	subs, err := d.Subs.ListReady(repository.ListFilter{
		Tags: tags, MinScore: minScore, StarredOnly: starred,
		Limit: 50, Offset: 0,
	})
	if err != nil {
		c.AbortWithStatus(500)
		return
	}

	views := make([]subView, 0, len(subs)+1)
	if newID != "" {
		if extra, err := d.Subs.Get(newID); err == nil && extra.Status != model.StatusReady {
			v := flatten(extra, newID)
			v.IsAdmin = admin
			views = append(views, v)
		}
	}
	for _, s := range subs {
		s := s
		v := flatten(&s, newID)
		v.IsAdmin = admin
		views = append(views, v)
	}

	data := gin.H{
		"Submissions": views,
		"MinScore":    minScore,
		"StarredOnly": starred,
		"NewID":       newID,
		"IsAdmin":     admin,
	}
	if fragment {
		render(c, d.View, "list_fragment", data)
		return
	}
	allTags, _ := d.Tags.AllWithCounts()
	data["Tags"] = allTags
	render(c, d.View, "dashboard", data)
}

func videoHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	view := flatten(s, "")
	render(c, d.View, "video", gin.H{
		"ID":          view.ID,
		"Status":      view.Status,
		"Summary":     view.Summary,
		"Transcript":  view.Transcript,
		"Tags":        view.Tags,
		"QualityScore": view.QualityScore,
		"DurationSec": s.DurationSec,
		"HasVideo":    s.VideoPath != nil,
		"HasAudio":    s.AudioPath != "",
	})
}

func statusHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	render(c, d.View, "status_fragment", gin.H{"Status": string(s.Status)})
}

func liveHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	v := flatten(s, "")
	render(c, d.View, "live_fragment", gin.H{
		"ID":           v.ID,
		"Status":       v.Status,
		"Summary":      v.Summary,
		"Transcript":   v.Transcript,
		"Tags":         v.Tags,
		"QualityScore": v.QualityScore,
	})
}

func cardHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	v := flatten(s, c.Query("new"))
	v.IsAdmin = isAdmin(c, d)
	render(c, d.View, "card_fragment", v)
}

func fileHandler(c *gin.Context, d *Deps, kind string) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	var path string
	switch kind {
	case "thumb":
		path = s.ThumbnailPath
	case "audio":
		path = s.AudioPath
	case "video":
		if s.VideoPath == nil {
			c.AbortWithStatus(404)
			return
		}
		path = *s.VideoPath
	}
	if path == "" {
		c.AbortWithStatus(404)
		return
	}
	http.ServeFile(c.Writer, c.Request, path)
}

func healthHandler(c *gin.Context, d *Deps) {
	depth, _ := d.Jobs.QueueDepth()
	c.JSON(200, gin.H{"status": "ok", "queue_depth": depth})
}

func clientIP(c *gin.Context) string {
	if v := c.GetHeader("CF-Connecting-IP"); v != "" {
		return v
	}
	return c.ClientIP()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func verifyTurnstile(secret, token, ip string) bool {
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	form.Set("remoteip", ip)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", form)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &out)
	return out.Success
}
