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

	"samqna/config"
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
	ExportSvc   *service.Export // wired in T18; nil OK for early route tests
}

// RegisterPublic mounts all unauthenticated routes onto r.
func RegisterPublic(r *gin.Engine, d *Deps) {
	r.StaticFS("/static", http.Dir("static"))

	r.GET("/", func(c *gin.Context) { render(c, d.View, "landing", gin.H{}) })
	r.GET("/submit", func(c *gin.Context) {
		render(c, d.View, "submit", gin.H{"TurnstileSite": d.Cfg.TurnstileSite})
	})
	r.POST("/submit", func(c *gin.Context) { submitHandler(c, d) })
	r.GET("/browse", func(c *gin.Context) { browseHandler(c, d, false) })
	r.GET("/browse/list", func(c *gin.Context) { browseHandler(c, d, true) })
	r.GET("/v/:id", func(c *gin.Context) { videoHandler(c, d) })
	r.GET("/v/:id/status", func(c *gin.Context) { statusHandler(c, d) })
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
			c.AbortWithStatus(400); return
		}
		c.Header("Content-Type", "video/mp4")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="clip-%s.mp4"`, c.Param("id")))
		if err := d.ExportSvc.Trim(c.Request.Context(), c.Param("id"), body.Start, body.End, c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
	r.POST("/export/batch", func(c *gin.Context) {
		var body struct{ IDs []string `json:"ids"` }
		if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
			c.AbortWithStatus(400); return
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

func submitHandler(c *gin.Context, d *Deps) {
	// Parse multipart first so we can read both file and form fields uniformly.
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
	// Turnstile (best-effort)
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
	c.Redirect(http.StatusSeeOther, "/v/"+res.ID)
}

func browseHandler(c *gin.Context, d *Deps, fragment bool) {
	tags := c.QueryArray("tag")
	minScore, _ := strconv.Atoi(c.DefaultQuery("min_score", "0"))
	starred := c.Query("starred") == "1"
	subs, err := d.Subs.ListReady(repository.ListFilter{
		Tags: tags, MinScore: minScore, StarredOnly: starred,
		Limit: 50, Offset: 0,
	})
	if err != nil {
		c.AbortWithStatus(500)
		return
	}
	type tagView struct{ Name string }
	type subView struct {
		ID           string
		Summary      string
		QualityScore int
		Tags         []tagView
	}
	views := make([]subView, 0, len(subs))
	for _, s := range subs {
		tv := make([]tagView, 0, len(s.Tags))
		for _, t := range s.Tags {
			tv = append(tv, tagView{Name: t.Name})
		}
		score := 0
		if s.QualityScore != nil {
			score = *s.QualityScore
		}
		views = append(views, subView{
			ID: s.ID, Summary: deref(s.Summary), QualityScore: score, Tags: tv,
		})
	}
	data := gin.H{
		"Submissions": views,
		"MinScore":    minScore,
		"StarredOnly": starred,
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
	type tagView struct{ Name string }
	tags := make([]tagView, 0, len(s.Tags))
	for _, t := range s.Tags {
		tags = append(tags, tagView{Name: t.Name})
	}
	render(c, d.View, "video", gin.H{
		"ID": s.ID, "Status": string(s.Status), "Summary": deref(s.Summary),
		"Transcript": deref(s.Transcript), "Tags": tags,
		"DurationSec": s.DurationSec,
		"HasVideo":    s.VideoPath != nil, "HasAudio": s.AudioPath != "",
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
		// fall back to allowing — don't block legit users on Cloudflare hiccups
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
