{
  "version": 1,
  "auth_id": "claude-legacy@example.com.json",
  "provider": "claude",
  "updated_at": "2026-10-19T18:16:29.763807869Z",
  "records": [
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "credential_quota",
      "quota": {
        "exceeded": true,
        "reason": "credential_quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T11:11:42.812755329-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441199",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "1",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792565999",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.98"
        }
      },
      "last_error": {
        "message": "{\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"This request would exceed your account's rate limit. Please try again later.\"},\"request_id\":\"req_011CgC2iwyrR6GRDB9CYEnoU\"}",
        "retryable": false,
        "http_status": 429
      },
      "updated_at": "2026-10-19T11:11:42.812755329-07:00"
    },
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "model": "claude-fable-5-1",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "credential_quota",
      "quota": {
        "exceeded": true,
        "reason": "credential_quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T10:59:13.55072545-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-5h-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-5h-Surpassed-Threshold": "0.9",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "0.99",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold": "0.75",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.97",
          "Anthropic-Ratelimit-Unified-7d_oi-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d_oi-Status": "allowed",
          "Anthropic-Ratelimit-Unified-7d_oi-Utilization": "0.12",
          "Anthropic-Ratelimit-Unified-Fallback": "available",
          "Anthropic-Ratelimit-Unified-Fallback-Percentage": "0.5",
          "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": "member_zero_credit_limit",
          "Anthropic-Ratelimit-Unified-Overage-Status": "rejected",
          "Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
          "Anthropic-Ratelimit-Unified-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-Status": "allowed_warning"
        }
      },
      "updated_at": "2026-10-19T10:58:56.141719429-07:00"
    },
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "model": "claude-haiku-5-5",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "credential_quota",
      "quota": {
        "exceeded": true,
        "reason": "credential_quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T09:05:56.016474411-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-5h-Status": "allowed",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "0.22",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold": "0.75",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.84",
          "Anthropic-Ratelimit-Unified-Fallback-Percentage": "0.5",
          "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": "member_zero_credit_limit",
          "Anthropic-Ratelimit-Unified-Overage-Status": "rejected",
          "Anthropic-Ratelimit-Unified-Representative-Claim": "seven_day",
          "Anthropic-Ratelimit-Unified-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-Status": "allowed_warning"
        }
      },
      "updated_at": "2026-10-19T09:05:56.016474411-07:00"
    },
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "model": "claude-opus-5-5",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "credential_quota",
      "quota": {
        "exceeded": true,
        "reason": "credential_quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T10:59:08.821889216-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-5h-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-5h-Surpassed-Threshold": "0.9",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "0.99",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold": "0.75",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.97",
          "Anthropic-Ratelimit-Unified-Fallback": "available",
          "Anthropic-Ratelimit-Unified-Fallback-Percentage": "0.5",
          "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": "member_zero_credit_limit",
          "Anthropic-Ratelimit-Unified-Overage-Status": "rejected",
          "Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
          "Anthropic-Ratelimit-Unified-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-Status": "allowed_warning"
        }
      },
      "updated_at": "2026-10-19T10:58:58.540164013-07:00"
    },
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "model": "claude-sonnet-5",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "quota",
      "quota": {
        "exceeded": true,
        "reason": "quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T10:59:01.315890402-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-5h-Status": "rejected",
          "Anthropic-Ratelimit-Unified-5h-Surpassed-Threshold": "1.0",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "1.0",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold": "0.75",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.97",
          "Anthropic-Ratelimit-Unified-Fallback-Percentage": "0.5",
          "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": "member_zero_credit_limit",
          "Anthropic-Ratelimit-Unified-Overage-Status": "rejected",
          "Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
          "Anthropic-Ratelimit-Unified-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-Status": "rejected",
          "Retry-After": "8458"
        }
      },
      "last_error": {
        "message": "{\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"This request would exceed your account's rate limit. Please try again later.\"},\"request_id\":\"req_011CgC2iwyrR6GRDB9CYEnoU\"}",
        "retryable": false,
        "http_status": 429
      },
      "updated_at": "2026-10-19T10:59:01.315890402-07:00"
    },
    {
      "provider": "claude",
      "auth_id": "claude-legacy@example.com.json",
      "model": "claude-sonnet-5-5",
      "status": "cooling",
      "next_retry_after": "2026-10-19T13:20:20.001233874-07:00",
      "reason": "credential_quota",
      "quota": {
        "exceeded": true,
        "reason": "credential_quota",
        "next_recover_at": "2026-10-19T13:20:20.001233874-07:00",
        "observed_at": "2026-10-19T10:58:26.341696929-07:00",
        "signals": {
          "Anthropic-Ratelimit-Unified-5h-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-5h-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-5h-Surpassed-Threshold": "0.9",
          "Anthropic-Ratelimit-Unified-5h-Utilization": "0.98",
          "Anthropic-Ratelimit-Unified-7d-Reset": "1792566000",
          "Anthropic-Ratelimit-Unified-7d-Status": "allowed_warning",
          "Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold": "0.75",
          "Anthropic-Ratelimit-Unified-7d-Utilization": "0.97",
          "Anthropic-Ratelimit-Unified-Fallback": "available",
          "Anthropic-Ratelimit-Unified-Fallback-Percentage": "0.5",
          "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": "member_zero_credit_limit",
          "Anthropic-Ratelimit-Unified-Overage-Status": "rejected",
          "Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
          "Anthropic-Ratelimit-Unified-Reset": "1792441200",
          "Anthropic-Ratelimit-Unified-Status": "allowed_warning"
        }
      },
      "updated_at": "2026-10-19T10:58:26.341696929-07:00"
    }
  ]
}
