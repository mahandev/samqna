package route

import (
	"crypto/subtle"
	"net/http"

	"samqna/service"

	"github.com/gin-gonic/gin"
)

func RegisterAdmin(r *gin.Engine, d *Deps) {
	adm := &service.Admin{Subs: d.Subs, Jobs: d.Jobs, IPs: d.IPs}
	g := r.Group("/admin", adminAuth(d.Cfg.AdminToken))
	g.POST("/v/:id/star", func(c *gin.Context) {
		if err := adm.ToggleStar(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/delete", func(c *gin.Context) {
		if err := adm.Delete(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/quarantine", func(c *gin.Context) {
		on := c.Query("on") != "0"
		if err := adm.Quarantine(c.Param("id"), on); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/requeue", func(c *gin.Context) {
		if err := adm.Requeue(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/block-ip", func(c *gin.Context) {
		var body struct{ IP, Reason string }
		if err := c.ShouldBindJSON(&body); err != nil { c.AbortWithStatus(400); return }
		if err := adm.BlockIP(body.IP, body.Reason); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.GET("/quarantine", func(c *gin.Context) {
		subs, err := d.Subs.ListQuarantined(50, 0)
		if err != nil { c.AbortWithStatus(500); return }
		c.JSON(200, subs)
	})
	g.GET("/jobs", func(c *gin.Context) {
		depth, _ := d.Jobs.QueueDepth()
		c.JSON(200, gin.H{"queue_depth": depth})
	})
}

func adminAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		given := c.GetHeader("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
			c.AbortWithStatus(http.StatusNotFound) // hide existence
			return
		}
		c.Next()
	}
}
