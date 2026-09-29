package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/sync/singleflight"
)

// Sub2APIUsageSnapshot describes the upstream API key, never the gateway's
// local traffic or the upstream's underlying Codex subscription.
type Sub2APIUsageSnapshot struct {
	Status     string            `json:"status"`
	ErrorCode  string            `json:"error_code,omitempty"`
	HTTPStatus int               `json:"http_status,omitempty"`
	UpdatedAt  *time.Time        `json:"updated_at,omitempty"`
	CheckedAt  time.Time         `json:"checked_at"`
	Stale      bool              `json:"stale"`
	Data       *Sub2APIUsageData `json:"data,omitempty"`
}

type Sub2APIUsageTotals struct {
	Requests    int64    `json:"requests"`
	TotalTokens int64    `json:"total_tokens"`
	Cost        *float64 `json:"cost,omitempty"`
	ActualCost  *float64 `json:"actual_cost,omitempty"`
}

type Sub2APIUsageQuota struct {
	Limit     float64    `json:"limit"`
	Used      float64    `json:"used"`
	Remaining float64    `json:"remaining"`
	Unit      string     `json:"unit,omitempty"`
	Window    string     `json:"window,omitempty"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

type Sub2APIUsageSubscription struct {
	DailyUsage   float64    `json:"daily_usage_usd"`
	WeeklyUsage  float64    `json:"weekly_usage_usd"`
	MonthlyUsage float64    `json:"monthly_usage_usd"`
	DailyLimit   *float64   `json:"daily_limit_usd,omitempty"`
	WeeklyLimit  *float64   `json:"weekly_limit_usd,omitempty"`
	MonthlyLimit *float64   `json:"monthly_limit_usd,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type Sub2APIUsageData struct {
	Mode     string `json:"mode"`
	IsValid  *bool  `json:"isValid"`
	Status   string `json:"status,omitempty"`
	PlanName string `json:"planName,omitempty"`
	// Zero balance is ambiguous in simple mode. Clients must not infer exhaustion.
	Balance      *float64                  `json:"balance,omitempty"`
	Unit         string                    `json:"unit,omitempty"`
	ExpiresAt    *time.Time                `json:"expires_at,omitempty"`
	Quota        *Sub2APIUsageQuota        `json:"quota,omitempty"`
	RateLimits   []Sub2APIUsageQuota       `json:"rate_limits,omitempty"`
	Subscription *Sub2APIUsageSubscription `json:"subscription,omitempty"`
	Usage        *struct {
		Today *Sub2APIUsageTotals `json:"today,omitempty"`
		Total *Sub2APIUsageTotals `json:"total,omitempty"`
	} `json:"usage,omitempty"`
}

type sub2APIUsageCacheEntry struct {
	identity string
	snapshot *Sub2APIUsageSnapshot
	retryAt  time.Time
}

type sub2APIUsageService struct {
	transport *AccountTestService
	cache     sync.Map // account ID -> immutable snapshot for the current credentials
	flight    singleflight.Group
}

func isSub2APIUsageCandidate(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey &&
		!upstreamBillingProbeTargetIsOfficialAPI(account.GetCredential("base_url")) &&
		account.Extra["sub2api_usage_enabled"] != false
}

func (s *sub2APIUsageService) get(ctx context.Context, account *Account, force bool) *Sub2APIUsageSnapshot {
	// Bind cache entries and in-flight requests to credential/proxy changes without
	// retaining the API key in the cache key or exposing it in logs.
	identityJSON, _ := json.Marshal([]any{account.Credentials, account.ProxyID, account.Proxy, account.Extra})
	identity := fmt.Sprintf("%x", sha256.Sum256(identityJSON))
	cached := func() *sub2APIUsageCacheEntry {
		if entry, ok := s.cache.Load(account.ID); ok {
			value := entry.(*sub2APIUsageCacheEntry)
			if value.identity == identity {
				return value
			}
		}
		return nil
	}
	reusable := func(entry *sub2APIUsageCacheEntry) bool {
		return entry != nil && time.Now().Before(entry.retryAt) &&
			(!force || time.Since(entry.snapshot.CheckedAt) < 5*time.Second)
	}
	if entry := cached(); reusable(entry) {
		return entry.snapshot
	}
	result, _, _ := s.flight.Do(fmt.Sprintf("%d:%s", account.ID, identity), func() (any, error) {
		if entry := cached(); reusable(entry) {
			return entry.snapshot, nil
		}
		snapshot := s.fetch(ctx, account)
		ttl := 3 * time.Minute
		if snapshot.Status != "ok" {
			ttl = time.Minute
			if snapshot.Status == "unsupported" {
				ttl = 30 * time.Minute
			}
			if previous := cached(); previous != nil && previous.snapshot.Data != nil {
				snapshot.Data = previous.snapshot.Data
				snapshot.UpdatedAt = previous.snapshot.UpdatedAt
				snapshot.Stale = true
			}
		}
		s.cache.Store(account.ID, &sub2APIUsageCacheEntry{identity, snapshot, time.Now().Add(ttl)})
		return snapshot, nil
	})
	return result.(*Sub2APIUsageSnapshot)
}

func (s *sub2APIUsageService) fetch(ctx context.Context, account *Account) *Sub2APIUsageSnapshot {
	snapshot := &Sub2APIUsageSnapshot{Status: "error", CheckedAt: time.Now().UTC()}
	fail := func(code string) *Sub2APIUsageSnapshot { snapshot.ErrorCode = code; return snapshot }
	if !isSub2APIUsageCandidate(account) {
		snapshot.Status = "unsupported"
		return fail("unsupported")
	}
	if s.transport == nil || s.transport.httpUpstream == nil {
		return fail("transport_unavailable")
	}
	baseURL, err := s.transport.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return fail("invalid_base_url")
	}
	apiKey := account.GetCredential("api_key")
	if strings.TrimSpace(apiKey) == "" {
		return fail("missing_api_key")
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return fail("proxy_unavailable")
		}
		proxyURL = account.Proxy.URL()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildOpenAIEndpointURL(baseURL, "/v1/usage"), nil)
	if err != nil {
		return fail("invalid_base_url")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Sub2API/usage-probe")
	account.ApplyHeaderOverrides(req.Header)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	var profile *tlsfingerprint.Profile
	if s.transport.tlsFPProfileService != nil {
		profile = s.transport.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.transport.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, profile)
	if err != nil {
		return fail("request_failed")
	}
	if resp == nil || resp.Body == nil {
		return fail("empty_response")
	}
	defer func() { _ = resp.Body.Close() }()
	snapshot.HTTPStatus = resp.StatusCode
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		snapshot.Status = "unsupported"
		return fail("unsupported")
	}
	if resp.StatusCode != http.StatusOK {
		// In particular, 403 may be a WAF response. Never mutate account status.
		return fail("http_error")
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fail("response_read_failed")
	}
	if len(body) > maxBody {
		return fail("response_too_large")
	}
	data, err := parseSub2APIUsage(body)
	if err != nil {
		snapshot.Status = "unsupported"
		return fail("invalid_response")
	}
	snapshot.Status = "ok"
	snapshot.Data = data
	snapshot.UpdatedAt = &snapshot.CheckedAt
	return snapshot
}

func parseSub2APIUsage(body []byte) (*Sub2APIUsageData, error) {
	var data Sub2APIUsageData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if data.IsValid == nil || (data.Mode != "unrestricted" && data.Mode != "quota_limited") ||
		(data.Usage == nil && data.Quota == nil && len(data.RateLimits) == 0 && data.Subscription == nil && data.Balance == nil) {
		return nil, fmt.Errorf("not a Sub2API usage response")
	}
	if len(data.RateLimits) > 16 || len(data.PlanName) > 256 || len(data.Status) > 64 || len(data.Unit) > 16 {
		return nil, fmt.Errorf("invalid usage metadata")
	}
	return &data, nil
}
