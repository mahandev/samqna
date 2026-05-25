package route

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"samqna/auth"
	"samqna/service"

	"github.com/gin-gonic/gin"
)

// adminContextKey marks a Gin context with the authenticated admin's
// identity (email from CF Access JWT, or "token" for script path).
const adminActorKey = "admin_actor"

func RegisterAdmin(r *gin.Engine, d *Deps) {
	adm := d.AdminSvc
	if adm == nil {
		adm = &service.Admin{
			DB: d.DB,
			Subs: d.Subs, Jobs: d.Jobs, Tags: d.Tags, IPs: d.IPs,
		}
	}

	g := r.Group("/admin", adminAuth(d.Cfg.AdminToken, d.CFAccess, d.GoogleAuth))

	g.GET("/", func(c *gin.Context) {
		audits, _ := adm.RecentAudits(200)
		blocked, _ := d.IPs.List()
		quarantined, _ := d.Subs.ListQuarantined(100, 0)
		depth, _ := d.Jobs.QueueDepth()
		render(c, d.View, "admin", gin.H{
			"Actor":       c.GetString(adminActorKey),
			"QueueDepth":  depth,
			"Paused":      adm.IsPaused(),
			"Audits":      audits,
			"Blocked":     blocked,
			"Quarantined": quarantined,
		})
	})

	g.POST("/v/:id/star", confirm(), func(c *gin.Context) {
		if err := adm.ToggleStar(c.GetString(adminActorKey), c.Param("id")); err != nil {
			c.AbortWithStatus(500)
			return
		}
		respondAfterMutation(c, d, c.Param("id"))
	})

	g.POST("/v/:id/delete", confirm(), func(c *gin.Context) {
		if err := adm.Delete(c.GetString(adminActorKey), c.Param("id")); err != nil {
			c.AbortWithStatus(500)
			return
		}
		// Deletion: HTMX swaps the card with nothing so it disappears.
		c.Header("HX-Reswap", "delete")
		c.Header("HX-Retarget", "#card-"+c.Param("id"))
		c.Status(200)
	})

	g.POST("/v/:id/quarantine", confirm(), func(c *gin.Context) {
		on := c.Query("on") != "0"
		if err := adm.Quarantine(c.GetString(adminActorKey), c.Param("id"), on); err != nil {
			c.AbortWithStatus(500)
			return
		}
		respondAfterMutation(c, d, c.Param("id"))
	})

	g.POST("/v/:id/requeue", confirm(), func(c *gin.Context) {
		if err := adm.Requeue(c.GetString(adminActorKey), c.Param("id")); err != nil {
			c.AbortWithStatus(500)
			return
		}
		respondAfterMutation(c, d, c.Param("id"))
	})

	g.POST("/v/:id/tags", confirm(), func(c *gin.Context) {
		var body struct {
			Tags []string `json:"tags"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.AbortWithStatus(400)
			return
		}
		if err := adm.EditTags(c.GetString(adminActorKey), c.Param("id"), body.Tags); err != nil {
			c.AbortWithStatus(500)
			return
		}
		respondAfterMutation(c, d, c.Param("id"))
	})

	g.POST("/block-ip", confirm(), func(c *gin.Context) {
		var body struct{ IP, Reason string }
		if err := c.ShouldBindJSON(&body); err != nil || body.IP == "" {
			c.AbortWithStatus(400)
			return
		}
		if err := adm.BlockIP(c.GetString(adminActorKey), body.IP, body.Reason); err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.Status(204)
	})

	g.POST("/unblock-ip", confirm(), func(c *gin.Context) {
		var body struct{ IP string }
		if err := c.ShouldBindJSON(&body); err != nil || body.IP == "" {
			c.AbortWithStatus(400)
			return
		}
		if err := adm.UnblockIP(c.GetString(adminActorKey), body.IP); err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.Status(204)
	})

	g.POST("/pause", confirm(), func(c *gin.Context) {
		if err := adm.Pause(c.GetString(adminActorKey)); err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.Status(204)
	})

	g.POST("/unpause", confirm(), func(c *gin.Context) {
		if err := adm.Unpause(c.GetString(adminActorKey)); err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.Status(204)
	})

	g.GET("/quarantine", func(c *gin.Context) {
		subs, err := d.Subs.ListQuarantined(50, 0)
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, subs)
	})

	g.GET("/jobs", func(c *gin.Context) {
		depth, _ := d.Jobs.QueueDepth()
		c.JSON(200, gin.H{"queue_depth": depth, "paused": adm.IsPaused()})
	})

	g.GET("/audit", func(c *gin.Context) {
		audits, err := adm.RecentAudits(200)
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, audits)
	})
}

// adminAuth lets a request through if EITHER:
//   - Cf-Access-Jwt-Assertion validates (browser path with CF Access), OR
//   - X-Admin-Token matches the configured static token (script path).
//
// On miss it returns 404 (NOT 401) so the existence of /admin is hidden
// from random scanners. The authenticated actor is stashed in the context
// under adminActorKey so handlers can record it in the audit log.
func adminAuth(token string, verifier *auth.Verifier, google *auth.GoogleAuth) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Google OAuth session cookie (the normal browser path)
		if google.Enabled() {
			if email, ok := google.ValidEmail(c.Request); ok {
				c.Set(adminActorKey, email)
				c.Next()
				return
			}
		}
		// 2. Cloudflare Access JWT (optional, only active if AUD configured)
		if verifier.Enabled() {
			if email, ok := verifier.ValidEmail(c.GetHeader("Cf-Access-Jwt-Assertion")); ok {
				c.Set(adminActorKey, email)
				c.Next()
				return
			}
		}
		// 3. Static token for scripts / curl
		given := c.GetHeader("X-Admin-Token")
		if token != "" && subtle.ConstantTimeCompare([]byte(given), []byte(token)) == 1 {
			c.Set(adminActorKey, "token")
			c.Next()
			return
		}
		// Browser fallback: send GETs (page loads) to /admin/login so the
		// signed-out creator can start the OAuth flow. JSON/HTMX/POST
		// requests still get 404 to hide existence.
		if google.Enabled() && c.Request.Method == http.MethodGet &&
			!strings.Contains(c.GetHeader("Accept"), "application/json") &&
			c.GetHeader("HX-Request") == "" {
			c.Redirect(http.StatusSeeOther, "/admin/login")
			c.Abort()
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}

// confirm requires X-Confirm: yes on destructive endpoints. Defense
// against XSS-style mistakes inside the admin UI itself — the HTMX
// buttons set this automatically; curl users have to be deliberate.
func confirm() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.EqualFold(c.GetHeader("X-Confirm"), "yes") {
			c.AbortWithStatus(http.StatusPreconditionRequired)
			return
		}
		c.Next()
	}
}

// respondAfterMutation re-renders the affected card and returns it to
// HTMX so the UI updates in place.
func respondAfterMutation(c *gin.Context, d *Deps, id string) {
	s, err := d.Subs.Get(id)
	if err != nil {
		c.AbortWithStatus(500)
		return
	}
	v := flatten(s, "")
	v.IsAdmin = true
	render(c, d.View, "card_fragment", v)
}
