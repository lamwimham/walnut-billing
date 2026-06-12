package handler

import (
	"net/http"
	"walnut-billing/internal/web"

	"github.com/gin-gonic/gin"
)

// ServeDashboard serves the embedded admin dashboard.
func ServeDashboard(c *gin.Context) {
	data, err := web.Assets.ReadFile("static/index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "Dashboard not found")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}
