package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

const sub2APIUsageFixture = `{"mode":"unrestricted","isValid":true,"balance":0,"usage":{"today":{"requests":12,"total_tokens":3400,"actual_cost":1.25},"total":{"requests":20,"total_tokens":5600,"actual_cost":2.5}},"secret":"must not be forwarded"}`

type sub2APIUsageHTTP struct {
	HTTPUpstream
	call func(*http.Request, string) (*http.Response, error)
}

func (s sub2APIUsageHTTP) DoWithTLS(req *http.Request, proxy string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.call(req, proxy)
}
func sub2APIUsageTestAccount() *Account {
	return &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: "active", Schedulable: true,
		Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "test-key"}}
}
func sub2APIUsageTestService(call func(*http.Request, string) (*http.Response, error)) *sub2APIUsageService {
	return &sub2APIUsageService{transport: &AccountTestService{cfg: &config.Config{}, httpUpstream: sub2APIUsageHTTP{call: call}}}
}
func sub2APIUsageResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}
func expireSub2APIUsageCache(s *sub2APIUsageService, id int64) {
	entry, _ := s.cache.Load(id)
	value := *entry.(*sub2APIUsageCacheEntry)
	value.retryAt = time.Time{}
	s.cache.Store(id, &value)
}

func TestSub2APIUsageParsing(t *testing.T) {
	for _, body := range []string{
		sub2APIUsageFixture,
		`{"mode":"quota_limited","isValid":true,"status":"quota_exhausted","quota":{"limit":10,"used":10,"remaining":0},"rate_limits":[{"window":"5h","limit":2,"used":1,"reset_at":"2026-09-29T12:00:00Z"}],"expires_at":"2026-10-01T00:00:00Z"}`,
		`{"mode":"unrestricted","isValid":true,"subscription":{"daily_usage_usd":1,"daily_limit_usd":10}}`,
	} {
		data, err := parseSub2APIUsage([]byte(body))
		require.NoError(t, err)
		require.NotNil(t, data)
		serialized, err := json.Marshal(data)
		require.NoError(t, err)
		require.NotContains(t, string(serialized), "must not be forwarded")
	}
	for _, body := range []string{`<html>login</html>`, `{}`, `null`, `{"mode":"unrestricted","balance":0}`, `{"isValid":true,"mode":"other","balance":0}`, `{"isValid":true,"mode":"unrestricted"}`, `{"isValid":true,"mode":"unrestricted","balance":"0"}`} {
		_, err := parseSub2APIUsage([]byte(body))
		require.Error(t, err, body)
	}
}

func TestSub2APIUsageEligibility(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com/v1", "https://API.OPENAI.COM.:443/v1", "https://ollama.com/v1"} {
		a := sub2APIUsageTestAccount()
		a.Credentials["base_url"] = base
		require.False(t, isSub2APIUsageCandidate(a), base)
	}
	a := sub2APIUsageTestAccount()
	require.True(t, isSub2APIUsageCandidate(a))
	a.Type = AccountTypeOAuth
	require.False(t, isSub2APIUsageCandidate(a))
	a.Type = AccountTypeAPIKey
	a.Extra = map[string]any{"sub2api_usage_enabled": false}
	require.False(t, isSub2APIUsageCandidate(a))
}

func TestSub2APIUsageRequestAndCache(t *testing.T) {
	for _, base := range []string{"https://relay.example", "https://relay.example/v1/", "https://relay.example/prefix/v1"} {
		t.Run(base, func(t *testing.T) {
			calls := 0
			s := sub2APIUsageTestService(func(req *http.Request, proxy string) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
				require.Equal(t, "Sub2API/usage-probe", req.Header.Get("User-Agent"))
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
				require.True(t, strings.HasSuffix(req.URL.Path, "/v1/usage"))
				require.NotContains(t, req.URL.Path, "/v1/v1/")
				require.Empty(t, proxy)
				return sub2APIUsageResponse(200, sub2APIUsageFixture), nil
			})
			a := sub2APIUsageTestAccount()
			a.Credentials["base_url"] = base
			result := s.get(context.Background(), a, false)
			require.Equal(t, "ok", result.Status)
			require.Equal(t, int64(12), result.Data.Usage.Today.Requests)
			require.NotNil(t, result.Data.Balance)
			require.Zero(t, *result.Data.Balance)
			require.Same(t, result, s.get(context.Background(), a, false))
			require.Same(t, result, s.get(context.Background(), a, true)) // minimum manual refresh cooldown
			require.Equal(t, 1, calls)
			expireSub2APIUsageCache(s, a.ID)
			s.get(context.Background(), a, false)
			require.Equal(t, 2, calls)
		})
	}
}

func TestSub2APIUsageFailureRetainsStaleAndNeverChangesAccount(t *testing.T) {
	for _, code := range []int{301, 401, 403, 404, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			s := sub2APIUsageTestService(func(_ *http.Request, _ string) (*http.Response, error) {
				calls++
				if calls == 1 {
					return sub2APIUsageResponse(200, sub2APIUsageFixture), nil
				}
				return sub2APIUsageResponse(code, `private upstream error`), nil
			})
			a := sub2APIUsageTestAccount()
			service := &AccountUsageService{sub2APIUsage: s}
			good, err := service.getUsageForAccount(context.Background(), a, false)
			require.NoError(t, err)
			expireSub2APIUsageCache(s, a.ID)
			result, err := service.getUsageForAccount(context.Background(), a, false)
			require.NoError(t, err)
			require.True(t, result.Sub2APIUsage.Stale)
			require.Same(t, good.Sub2APIUsage.Data, result.Sub2APIUsage.Data)
			require.Equal(t, code, result.Sub2APIUsage.HTTPStatus)
			require.NotContains(t, result.Sub2APIUsage.ErrorCode, "private")
			require.True(t, a.Schedulable)
			require.Equal(t, "active", a.Status)
			s.get(context.Background(), a, false)
			require.Equal(t, 2, calls)
			// A new key must never inherit old-key data, even if its first probe fails.
			a.Credentials["api_key"] = "replacement-key"
			replaced := s.get(context.Background(), a, false)
			require.Nil(t, replaced.Data)
			require.False(t, replaced.Stale)
		})
	}
}

func TestSub2APIUsageConcurrentRequestsCoalesce(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	s := sub2APIUsageTestService(func(_ *http.Request, _ string) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return sub2APIUsageResponse(200, sub2APIUsageFixture), nil
	})
	a := sub2APIUsageTestAccount()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); s.get(context.Background(), a, false) }()
	}
	<-entered
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}

func TestSub2APIUsageResponseLimitsAndProxy(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", (1<<20)+1), `{"mode":"unrestricted","isValid":true}`} {
		s := sub2APIUsageTestService(func(_ *http.Request, _ string) (*http.Response, error) { return sub2APIUsageResponse(200, body), nil })
		result := s.get(context.Background(), sub2APIUsageTestAccount(), false)
		require.Nil(t, result.Data)
		require.NotEqual(t, "ok", result.Status)
	}
	a := sub2APIUsageTestAccount()
	id := int64(9)
	a.ProxyID = &id
	s := sub2APIUsageTestService(func(_ *http.Request, _ string) (*http.Response, error) {
		t.Fatal("must not bypass unavailable proxy")
		return nil, nil
	})
	require.Equal(t, "proxy_unavailable", s.get(context.Background(), a, false).ErrorCode)
}
