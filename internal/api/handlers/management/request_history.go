package management

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
// The service reads RuntimeConfig through the manager it was started with, so the
// handler does not touch h.mu, h.cfg or h.authManager.
func (h *Handler) GetRequestHistory(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	f, err := parseRequestFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, requestLogService().Query(f))
}

// GetRequestSessions serves GET /v8/management/observability/requests/sessions.
//
// Query: key, auth, model, from, to, limit.
func (h *Handler) GetRequestSessions(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	f, err := parseSessionFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, requestLogService().Sessions(f))
}

func parseRequestFilter(c *gin.Context) (requestlog.Filter, error) {
	f := requestlog.Filter{
		Session: strings.TrimSpace(c.Query("session")),
		KeyHash: strings.TrimSpace(c.Query("key")),
		AuthID:  strings.TrimSpace(c.Query("auth")),
		Model:   strings.TrimSpace(c.Query("model")),
	}
	var err error
	if f.Status, err = parseStatusToken(c.Query("status")); err != nil {
		return f, err
	}
	if f.Verdict, err = parseVerdictTokens(c.Query("verdict")); err != nil {
		return f, err
	}
	if f.From, err = parseTimeParam("from", c.Query("from")); err != nil {
		return f, err
	}
	if f.To, err = parseTimeParam("to", c.Query("to")); err != nil {
		return f, err
	}
	if f.Since, err = parseSeqParam("since", c.Query("since")); err != nil {
		return f, err
	}
	if f.Before, err = parseSeqParam("before", c.Query("before")); err != nil {
		return f, err
	}
	if f.Limit, err = parseLimitParam(c.Query("limit")); err != nil {
		return f, err
	}
	switch order := strings.ToLower(strings.TrimSpace(c.Query("order"))); order {
	case "", "desc":
	case "asc":
		f.Asc = true
	default:
		return f, errors.New("order must be asc or desc")
	}
	return f, nil
}

func parseSessionFilter(c *gin.Context) (requestlog.SessionFilter, error) {
	f := requestlog.SessionFilter{
		KeyHash: strings.TrimSpace(c.Query("key")),
		AuthID:  strings.TrimSpace(c.Query("auth")),
		Model:   strings.TrimSpace(c.Query("model")),
	}
	var err error
	if f.From, err = parseTimeParam("from", c.Query("from")); err != nil {
		return f, err
	}
	if f.To, err = parseTimeParam("to", c.Query("to")); err != nil {
		return f, err
	}
	if f.Limit, err = parseLimitParam(c.Query("limit")); err != nil {
		return f, err
	}
	return f, nil
}

// parseStatusToken accepts "", ok, failed, or an HTTP status code.
func parseStatusToken(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "ok", "failed":
		return value, nil
	}
	if code, err := strconv.Atoi(value); err != nil || code < 100 || code > 599 {
		return "", errors.New("status must be ok, failed or an HTTP status code")
	}
	return value, nil
}

var verdictTokens = map[string]bool{"hit": true, "partial": true, "miss": true, "cold": true, "na": true}

// parseVerdictTokens validates a csv of verdict names and returns it normalized.
func parseVerdictTokens(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(part))
		if !verdictTokens[parts[i]] {
			return "", errors.New("verdict must be a comma-separated list of hit, partial, miss, cold, na")
		}
	}
	return strings.Join(parts, ","), nil
}

// parseTimeParam accepts RFC3339 or unix milliseconds.
func parseTimeParam(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New(name + " must be RFC3339 or unix milliseconds")
	}
	return t, nil
}

func parseSeqParam(name, value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	seq, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New(name + " must be a non-negative integer")
	}
	return seq, nil
}

func parseLimitParam(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, errors.New("limit must be a positive integer")
	}
	return limit, nil
}
