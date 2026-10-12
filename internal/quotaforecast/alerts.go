package quotaforecast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	// alertFireAfter is how many consecutive evaluations must project a shortfall
	// before an alert fires, so a single noisy sample cannot trigger it.
	alertFireAfter = 3
	// alertResolveAfter is how many consecutive clear evaluations resolve an alert.
	alertResolveAfter = 10
	// webhookTimeout bounds the alert webhook POST so a hung receiver cannot pin a goroutine.
	webhookTimeout = 10 * time.Second
)

// evaluateAlerts advances the per-class alert state machine with the latest pool
// forecasts and returns the alerts that fired on this evaluation. An alert fires at
// most once per reset cycle: after firing it stays suppressed until the reset the
// pool was waiting for has passed.
func (h *history) evaluateAlerts(pools []Pool, now time.Time, lead time.Duration) []alertRecord {
	nowUnix := now.Unix()
	byClass := make(map[string]Pool, len(pools))
	for _, p := range pools {
		byClass[p.Class] = p
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	classes := make(map[string]struct{}, len(pools)+len(h.alerts))
	for class := range byClass {
		classes[class] = struct{}{}
	}
	for class := range h.alerts {
		classes[class] = struct{}{}
	}
	var fired []alertRecord
	for class := range classes {
		rec := h.alerts[class]
		if rec != nil && rec.State != alertPending && rec.RearmAt > 0 && nowUnix >= rec.RearmAt {
			delete(h.alerts, class)
			h.dirty = true
			rec = nil
		}
		pool, ok := byClass[class]
		shortfall := ok && pool.Burn.Sufficient && pool.Runway.ExhaustsAt != nil &&
			(lead <= 0 || pool.Runway.ExhaustsAt.Sub(now) <= lead)
		if !shortfall {
			if rec == nil {
				continue
			}
			switch rec.State {
			case alertPending:
				delete(h.alerts, class)
			case alertFiring:
				rec.Clear++
				if rec.Clear >= alertResolveAfter {
					rec.State = alertResolved
				}
			}
			h.dirty = true
			continue
		}
		if rec == nil {
			rec = &alertRecord{Class: class, Provider: pool.Provider, State: alertPending}
			h.alerts[class] = rec
		}
		rec.Clear = 0
		rec.ExhaustsAt = pool.Runway.ExhaustsAt.Unix()
		rec.RecoversAt = 0
		if pool.Runway.RecoversAt != nil {
			rec.RecoversAt = pool.Runway.RecoversAt.Unix()
		}
		rec.ShortAccounts = pool.Runway.ShortAccounts
		rec.RemainingAccounts = pool.RemainingAccounts
		h.dirty = true
		switch rec.State {
		case alertPending:
			rec.Consecutive++
			if rec.Consecutive < alertFireAfter {
				continue
			}
			rec.State = alertFiring
			rec.FiredAt = nowUnix
			rec.RearmAt = rec.RecoversAt
			if rec.RearmAt == 0 {
				rec.RearmAt = nowUnix + max(pool.WindowSeconds, int64(minHorizon/time.Second))
			}
			fired = append(fired, *rec)
		case alertResolved:
			// Still inside the cycle that already notified: show it again, quietly.
			rec.State = alertFiring
		}
	}
	return fired
}

// alertsForResponse converts the recorded alerts that are firing or resolved.
func alertsForResponse(records []alertRecord) []Alert {
	out := make([]Alert, 0, len(records))
	for _, rec := range records {
		if rec.State == alertPending {
			continue
		}
		out = append(out, Alert{
			Class:             rec.Class,
			Provider:          rec.Provider,
			State:             rec.State,
			FiredAt:           unixPtr(rec.FiredAt),
			ExhaustsAt:        unixPtr(rec.ExhaustsAt),
			RecoversAt:        unixPtr(rec.RecoversAt),
			ShortAccounts:     rec.ShortAccounts,
			RemainingAccounts: rec.RemainingAccounts,
		})
	}
	return out
}

// webhookPayload is the JSON body POSTed to quota-forecast.alert-webhook-url. Text
// makes it directly usable by Slack-compatible receivers.
type webhookPayload struct {
	Event             string     `json:"event"`
	Class             string     `json:"class"`
	Provider          string     `json:"provider"`
	Window            string     `json:"window"`
	FiredAt           time.Time  `json:"fired_at"`
	ExhaustsAt        *time.Time `json:"exhausts_at"`
	RecoversAt        *time.Time `json:"recovers_at"`
	ShortSeconds      int64      `json:"short_seconds"`
	ShortAccounts     float64    `json:"short_accounts"`
	RemainingAccounts float64    `json:"remaining_accounts"`
	Text              string     `json:"text"`
}

func newWebhookPayload(rec alertRecord, now time.Time) webhookPayload {
	payload := webhookPayload{
		Event:             "quota_exhaustion_projected",
		Class:             rec.Class,
		Provider:          rec.Provider,
		Window:            classWindow(rec.Class),
		FiredAt:           now.UTC(),
		ExhaustsAt:        unixPtr(rec.ExhaustsAt),
		RecoversAt:        unixPtr(rec.RecoversAt),
		ShortAccounts:     rec.ShortAccounts,
		RemainingAccounts: rec.RemainingAccounts,
	}
	text := fmt.Sprintf("%s %s quota pool is projected to run out in %s",
		rec.Provider, payload.Window, formatSpan(time.Unix(rec.ExhaustsAt, 0).Sub(now)))
	if rec.RecoversAt > rec.ExhaustsAt {
		payload.ShortSeconds = rec.RecoversAt - rec.ExhaustsAt
		text += fmt.Sprintf(", %s before the next reset", formatSpan(time.Duration(payload.ShortSeconds)*time.Second))
	}
	payload.Text = text + fmt.Sprintf(" (about %.1f accounts short, %.1f left now)", rec.ShortAccounts, rec.RemainingAccounts)
	return payload
}

// notifyAlert logs a fired alert and POSTs it to the configured webhook.
func notifyAlert(cfg *config.Config, rec alertRecord, now time.Time) {
	payload := newWebhookPayload(rec, now)
	log.WithFields(log.Fields{
		"class":          rec.Class,
		"exhausts_at":    payload.ExhaustsAt,
		"recovers_at":    payload.RecoversAt,
		"short_accounts": rec.ShortAccounts,
	}).Warn("quota pool projected to exhaust before reset")
	if cfg == nil {
		return
	}
	endpoint := strings.TrimSpace(cfg.QuotaForecast.AlertWebhookURL)
	if endpoint == "" {
		return
	}
	if err := postWebhook(&cfg.SDKConfig, endpoint, payload); err != nil {
		log.WithError(err).WithField("class", rec.Class).Warn("quota forecast alert webhook failed")
	}
}

func postWebhook(sdkCfg *config.SDKConfig, endpoint string, payload webhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), webhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		// The URL may embed a secret token, so it is never included in the error.
		return fmt.Errorf("build webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	client := util.SetProxy(sdkCfg, &http.Client{Timeout: webhookTimeout})
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook request: %s", redactURLError(err))
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close webhook response body")
		}
	}()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// redactURLError drops the request URL, which may embed a secret token, from err.
func redactURLError(err error) string {
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// formatSpan renders a duration as a short "1d 4h" / "3h 20m" / "12m" string.
func formatSpan(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	minutes := int(d % time.Hour / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
