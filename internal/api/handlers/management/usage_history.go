package management

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

// usageHistorySource is the part of *usagehistory.Service the handlers use.
type usageHistorySource interface {
	Report(ctx context.Context, q usagehistory.Query) (usagehistory.Report, error)
	Prices(ctx context.Context) usagehistory.PriceReport
}

// usageHistoryService is the usage history source; tests replace it.
var usageHistoryService = func() usageHistorySource { return usagehistory.Default() }

// GetUsageHistory serves GET /v8/management/observability/usage/history.
//
// Query: from, to (YYYY-MM-DD, inclusive), tz (IANA), bucket (hour|day|month),
// group (model|provider|key|auth|agent|none), provider, model, key, auth, agent.
// 400 on anything ParseQuery rejects and on a report over the cell limit.
// Disabled -> 200 {enabled:false, reason}.
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
	rep, err := usageHistoryService().Report(c.Request.Context(), q)
	if err != nil {
		var tooLarge usagehistory.ErrTooLarge
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rep)
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
