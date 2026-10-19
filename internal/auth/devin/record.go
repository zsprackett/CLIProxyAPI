package devin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// CreateAuthRecord builds the shared CLI and management OAuth credential record.
// Profile and quota enrichment are best-effort; the session token is permanent.
func (s *DevinAuthService) CreateAuthRecord(ctx context.Context, token string) (*coreauth.Auth, error) {
	sessionToken := FormatSessionToken(token)
	if sessionToken == "" {
		return nil, fmt.Errorf("devin session token is required")
	}
	userName, userID, orgID, errSelf := s.FetchSelfProfile(ctx, sessionToken)
	if errSelf != nil {
		log.Warn("failed to fetch devin user profile")
	}
	userStatus, errStatus := s.FetchUserStatus(ctx, sessionToken, "")
	if errStatus != nil {
		log.Warn("failed to fetch devin user status and quota")
	}
	if errCtx := ctx.Err(); errCtx != nil {
		return nil, errCtx
	}

	var email, plan string
	if userStatus != nil {
		if userName == "" {
			userName = userStatus.UserName
		}
		if userID == "" {
			userID = userStatus.UserID
		}
		if orgID == "" {
			orgID = userStatus.OrgID
		}
		email = userStatus.Email
		plan = userStatus.Plan
	}

	identifier := userName
	if identifier == "" {
		identifier = userID
	}
	if identifier == "" {
		// Avoid overwriting another account when profile lookup is unavailable.
		digest := sha256.Sum256([]byte(sessionToken))
		identifier = fmt.Sprintf("user-%x", digest[:8])
	}
	// Upstream profile values must never introduce path components into filenames.
	fileIdentifier := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '@' {
			return r
		}
		return '_'
	}, identifier)
	if fileIdentifier != identifier || len(fileIdentifier) > 160 {
		digest := sha256.Sum256([]byte(identifier))
		fileIdentifier = fmt.Sprintf("user-%x", digest[:8])
	}
	fileName := fmt.Sprintf("devin-%s.json", fileIdentifier)
	label := fmt.Sprintf("Devin (%s)", identifier)
	if email != "" {
		label = fmt.Sprintf("Devin (%s - %s)", identifier, email)
	}

	attributes := map[string]string{
		"api_key":       sessionToken,
		"session_token": sessionToken,
		"user_name":     userName,
		"user_id":       userID,
		"org_id":        orgID,
		"base_url":      DefaultServerURL,
		"auth_kind":     "oauth",
	}
	metadata := map[string]any{
		"type":          "devin",
		"api_key":       sessionToken,
		"session_token": sessionToken,
		"user_name":     userName,
		"user_id":       userID,
		"org_id":        orgID,
		"auth_kind":     "oauth",
	}
	if email != "" {
		attributes["email"] = email
		metadata["email"] = email
	}
	if plan != "" {
		attributes["plan"] = plan
		metadata["plan"] = plan
	}

	record := &coreauth.Auth{
		ID:         fileName,
		Provider:   "devin",
		FileName:   fileName,
		Label:      label,
		Status:     coreauth.StatusActive,
		Attributes: attributes,
		Metadata:   metadata,
	}
	if userStatus != nil {
		record.QuotaSnapshot.Observe(coreauth.DevinStatusObservation(coreauth.SourceRefresh, plan,
			userStatus.DailyQuotaRemainingPercent, userStatus.WeeklyQuotaRemainingPercent,
			userStatus.DailyQuotaResetAt, userStatus.WeeklyQuotaResetAt, userStatus.PlanStart, userStatus.PlanEnd, time.Now()))
	}
	return record, nil
}
