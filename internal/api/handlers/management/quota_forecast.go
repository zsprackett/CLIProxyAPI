package management

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotaforecast"
)

// quotaForecastService is the forecast source; tests replace it.
var quotaForecastService = quotaforecast.Default

// GetQuotaForecast returns pooled quota trends, runway, pace, attribution and alerts.
// Query parameters: provider (adds charts and detail for one provider),
// lookback_hours (6-24) and history_hours.
func (h *Handler) GetQuotaForecast(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	lookback, errLookback := parseOptionalHours(c.Query("lookback_hours"))
	if errLookback != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lookback_hours must be a positive integer"})
		return
	}
	history, errHistory := parseOptionalHours(c.Query("history_hours"))
	if errHistory != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "history_hours must be a positive integer"})
		return
	}

	h.mu.Lock()
	manager := h.authManager
	cfg := h.cfg
	h.mu.Unlock()
	if manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	if runtimeCfg := manager.RuntimeConfig(); runtimeCfg != nil {
		cfg = runtimeCfg
	}
	c.JSON(http.StatusOK, quotaForecastService().Forecast(c.Request.Context(), cfg, manager.List(), quotaforecast.ForecastOptions{
		Provider:      strings.TrimSpace(c.Query("provider")),
		LookbackHours: lookback,
		HistoryHours:  history,
	}))
}

func parseOptionalHours(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	hours, err := strconv.Atoi(value)
	if err != nil || hours <= 0 {
		return 0, strconv.ErrSyntax
	}
	return hours, nil
}
