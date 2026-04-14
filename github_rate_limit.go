package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var sharedGitHubRateLimiter = newGitHubRateLimiter(githubMutatingRequestSpacing)

type githubRateLimiter struct {
	mutatingMinSpacing time.Duration

	mu             sync.Mutex
	lastMutatingAt time.Time
	retryUntil     time.Time
	conditional    map[string]conditionalResponse
}

type conditionalResponse struct {
	body         []byte
	header       http.Header
	etag         string
	lastModified string
}

type githubRateLimitTransport struct {
	base    http.RoundTripper
	limiter *githubRateLimiter
}

func newGitHubRateLimiter(minSpacing time.Duration) *githubRateLimiter {
	return &githubRateLimiter{
		mutatingMinSpacing: minSpacing,
		conditional:        map[string]conditionalResponse{},
	}
}

func (l *githubRateLimiter) wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return githubRateLimitTransport{base: base, limiter: l}
}

func (l *githubRateLimiter) wait(ctx context.Context, req *http.Request) error {
	for {
		l.mu.Lock()
		now := time.Now()
		next := l.retryUntil
		if isMutatingMethod(req.Method) {
			if spaced := l.lastMutatingAt.Add(l.mutatingMinSpacing); spaced.After(next) {
				next = spaced
			}
		}
		if !next.After(now) {
			if isMutatingMethod(req.Method) {
				l.lastMutatingAt = now
			}
			l.mu.Unlock()
			return nil
		}
		wait := next.Sub(now)
		l.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *githubRateLimiter) noteRetryAfter(wait time.Duration) {
	if wait <= 0 {
		return
	}
	until := time.Now().Add(wait)
	l.mu.Lock()
	if until.After(l.retryUntil) {
		l.retryUntil = until
	}
	l.mu.Unlock()
}

func (l *githubRateLimiter) noteSuccess(req *http.Request) {
	l.mu.Lock()
	if isMutatingMethod(req.Method) {
		clear(l.conditional)
	}
	l.mu.Unlock()
}

func (l *githubRateLimiter) cooldownRemaining() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.retryUntil.After(time.Now()) {
		return 0
	}
	return time.Until(l.retryUntil)
}

func (l *githubRateLimiter) cachedConditional(req *http.Request) (conditionalResponse, bool) {
	if req.Method != http.MethodGet {
		return conditionalResponse{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cached, ok := l.conditional[conditionalCacheKey(req.URL)]
	return cached, ok
}

func (l *githubRateLimiter) storeConditional(req *http.Request, resp *http.Response) {
	if req.Method != http.MethodGet || resp == nil || resp.StatusCode != http.StatusOK {
		return
	}

	body, err := readAndReplaceBody(resp)
	if err != nil {
		return
	}
	etag := resp.Header.Get("ETag")
	lastModified := resp.Header.Get("Last-Modified")
	if etag == "" && lastModified == "" {
		return
	}

	l.mu.Lock()
	l.conditional[conditionalCacheKey(req.URL)] = conditionalResponse{
		body:         bytes.Clone(body),
		header:       resp.Header.Clone(),
		etag:         etag,
		lastModified: lastModified,
	}
	l.mu.Unlock()
}

func (l *githubRateLimiter) synthesizeCachedResponse(
	req *http.Request,
	resp *http.Response,
	cached conditionalResponse,
) *http.Response {
	headers := cached.header.Clone()
	for k, vals := range resp.Header {
		headers[k] = append([]string(nil), vals...)
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Header:        headers,
		Body:          io.NopCloser(bytes.NewReader(cached.body)),
		ContentLength: int64(len(cached.body)),
		Request:       req,
	}
}

func (t githubRateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.wait(req.Context(), req); err != nil {
		return nil, err
	}

	cached, ok := t.limiter.cachedConditional(req)
	if ok {
		if cached.etag != "" {
			req.Header.Set("If-None-Match", cached.etag)
		}
		if cached.etag == "" && cached.lastModified != "" {
			req.Header.Set("If-Modified-Since", cached.lastModified)
		}
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	if resp.StatusCode == http.StatusNotModified && ok {
		t.limiter.noteSuccess(req)
		return t.limiter.synthesizeCachedResponse(req, resp, cached), nil
	}

	if wait, ok := secondaryRateLimitRetry(resp); ok {
		t.limiter.noteRetryAfter(wait)
		return resp, nil
	}

	t.limiter.noteSuccess(req)
	t.limiter.storeConditional(req, resp)
	return resp, nil
}

func secondaryRateLimitRetry(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}

	body, err := readAndReplaceBody(resp)
	if err != nil {
		return 0, false
	}

	if !isSecondaryRateLimit(resp.StatusCode, resp.Header, body) {
		return 0, false
	}
	if wait, ok := parseRetryAfter(resp.Header.Get(headerRetryAfter)); ok {
		return wait, true
	}
	if wait, ok := parseRateLimitReset(resp.Header); ok {
		return wait, true
	}
	return githubSecondaryRetryFallback, true
}

func readAndReplaceBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		return nil, closeErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return body, nil
}

func isSecondaryRateLimit(status int, headers http.Header, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if headers.Get(headerRetryAfter) != "" {
		return true
	}
	if strings.TrimSpace(headers.Get(headerRateLimitRemaining)) == "0" {
		return true
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	msg := strings.ToLower(payload.Message)
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "abuse detection")
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second, true
	}
	if ts, err := http.ParseTime(value); err == nil {
		if wait := time.Until(ts); wait > 0 {
			return wait, true
		}
		return 0, true
	}
	return 0, false
}

func parseRateLimitReset(headers http.Header) (time.Duration, bool) {
	if strings.TrimSpace(headers.Get(headerRateLimitRemaining)) != "0" {
		return 0, false
	}
	reset := strings.TrimSpace(headers.Get(headerRateLimitReset))
	if reset == "" {
		return 0, false
	}
	epoch, err := strconv.ParseInt(reset, 10, 64)
	if err != nil {
		return 0, false
	}
	wait := time.Until(time.Unix(epoch, 0).Add(githubRateLimitResetSkew))
	if wait < 0 {
		return 0, true
	}
	return wait, true
}

func conditionalCacheKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func githubAPICooldown() time.Duration {
	return sharedGitHubRateLimiter.cooldownRemaining()
}

func refreshCooldownDelay(base time.Duration) time.Duration {
	return max(base, githubAPICooldown())
}
