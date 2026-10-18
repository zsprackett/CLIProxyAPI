package management

import (
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog"
)

// requestLogService is the request history source; tests replace it.
var requestLogService = requestlog.Default

// GetRequestHistory serves GET /v8/management/observability/requests.
//
// Query: session, key, auth, model, status (ok|failed|<code>), verdict (csv),
// from, to (RFC3339 or unix ms), since (seq cursor), before (seq), limit, order (asc|desc).
// 400 on unparseable numbers/times or an unknown verdict/status token.
//
//	f, err := parseRequestFilter(c)
//	if err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
//	c.JSON(http.StatusOK, requestLogService().Query(f))
//
// The service reads RuntimeConfig through the manager it was started with, so the
// handler does not touch h.mu, h.cfg or h.authManager.
func (h *Handler) GetRequestHistory(c *gin.Context) { panic("not implemented") }

// GetRequestSessions serves GET /v8/management/observability/requests/sessions.
//
// Query: key, auth, model, from, to, limit.
//
//	f, err := parseSessionFilter(c)
//	if err != nil { c.JSON(400, ...); return }
//	c.JSON(http.StatusOK, requestLogService().Sessions(f))
func (h *Handler) GetRequestSessions(c *gin.Context) { panic("not implemented") }

func parseRequestFilter(c *gin.Context) (requestlog.Filter, error)        { panic("not implemented") }
func parseSessionFilter(c *gin.Context) (requestlog.SessionFilter, error) { panic("not implemented") }

// internal/api/server_management_v8.go, next to /observability/quota/forecast:
//
//	v8.GET("/observability/requests", s.mgmt.GetRequestHistory)
//	v8.GET("/observability/requests/sessions", s.mgmt.GetRequestSessions)
//
// Route note: /observability/logs/requests/:id already exists (full dump by request
// id). If its id is the inbound request id (unverified, see README open questions),
// Entry.trace_id lets the UI link a row to its full dump.
