package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

// usageHistoryService is the usage history source; tests replace it.
var usageHistoryService = usagehistory.Default

// GetUsageHistory serves GET /v8/management/observability/usage/history.
//
// Query: from, to (YYYY-MM-DD, inclusive), tz (IANA), bucket (hour|day|month),
// group (model|provider|key|auth|agent|none), provider, model, key, auth, agent.
// 400 on anything ParseQuery rejects. Disabled -> 200 {enabled:false, reason}.
func (h *Handler) GetUsageHistory(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	q, err := usagehistory.ParseQuery(c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, usageHistoryService().Report(c.Request.Context(), q))
}

// GetUsagePrices serves GET /v8/management/observability/usage/prices: the
// effective price per (provider, model) in history or overrides, its source
// (builtin|remote|override) and the catalog's fetch state.
func (h *Handler) GetUsagePrices(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	c.JSON(http.StatusOK, usageHistoryService().Prices(c.Request.Context()))
}

// internal/api/server_management_v8.go, next to the other observability routes:
//
//	v8.GET("/observability/usage/history", s.mgmt.GetUsageHistory)
//	v8.GET("/observability/usage/prices", s.mgmt.GetUsagePrices)
//
// No collision with /observability/usage/queue or /observability/usage/api-keys.
// /v0/management untouched.
