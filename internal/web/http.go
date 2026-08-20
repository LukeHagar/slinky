package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0 Safari/537.36"
const maxRedirects = 10
const maxRetries = 3

// fetchWithMethod performs HTTP request with retry logic and redirect following.
// Returns: (isOK, statusCode, response, error)
// Status code handling:
//   - OK (don't flag): 200-299, 401 (Unauthorized), 403 (Forbidden) - page exists
//   - Bad (flag): 404 (Not Found), DNS errors, connection refused
//   - Retry then flag: 408 (Timeout), 429 (Rate Limited), 5xx (Server Errors)
func fetchWithMethod(ctx context.Context, client *http.Client, method string, raw string) (bool, int, *http.Response, error) {
	var lastErr error
	var lastResp *http.Response
	
	// Retry logic for transient errors
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return false, 0, nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		
		req, err := http.NewRequestWithContext(ctx, method, raw, nil)
		if err != nil {
			return false, 0, nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Accept", "*/*")
		
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			// Check if error is retryable
			if isDNSError(err) || isRefused(err) {
				// Non-retryable: DNS errors and connection refused are permanent failures
				return false, 404, nil, simpleError("host not found")
			}
			if isTimeout(err) {
				// Retryable: timeouts may be transient
				if attempt < maxRetries {
					continue
				}
				return false, 408, nil, simpleError("request timeout")
			}
			// Other network errors: retry if we have attempts left
			if attempt < maxRetries {
				continue
			}
			return false, 0, nil, err
		}
		
		// Follow redirects and check final status
		finalResp, finalStatus, redirectErr := followRedirects(ctx, client, resp, raw, 0)
		if redirectErr != nil {
			// Redirect error: retry if transient
			if attempt < maxRetries && (isTimeout(redirectErr) || isRetryableStatus(finalStatus)) {
				if finalResp != nil && finalResp.Body != nil {
					finalResp.Body.Close()
				}
				continue
			}
			if finalResp != nil && finalResp.Body != nil {
				finalResp.Body.Close()
			}
			return false, finalStatus, nil, redirectErr
		}
		
		lastResp = finalResp
		status := finalResp.StatusCode
		
		// Check if status is retryable
		if isRetryableStatus(status) && attempt < maxRetries {
			if finalResp.Body != nil {
				finalResp.Body.Close()
			}
			continue
		}
		
		// Determine if link is OK based on status code
		isOK := isOKStatus(status)
		return isOK, status, finalResp, nil
	}
	
	// All retries exhausted
	if lastResp != nil && lastResp.Body != nil {
		lastResp.Body.Close()
	}
	return false, 0, nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// followRedirects follows redirects up to maxRedirects, checking for loops.
func followRedirects(ctx context.Context, client *http.Client, resp *http.Response, originalURL string, depth int) (*http.Response, int, error) {
	if depth > maxRedirects {
		return resp, resp.StatusCode, simpleError("too many redirects")
	}
	
	status := resp.StatusCode
	if status < 300 || status >= 400 {
		// Not a redirect
		return resp, status, nil
	}
	
	// Handle redirect
	location := resp.Header.Get("Location")
	if location == "" {
		return resp, status, nil
	}
	
	// Resolve relative URLs
	baseURL, err := url.Parse(originalURL)
	if err != nil {
		return resp, status, err
	}
	redirectURL, err := baseURL.Parse(location)
	if err != nil {
		return resp, status, err
	}
	
	// Check for redirect loop (simple check: same URL)
	if redirectURL.String() == originalURL {
		return resp, status, simpleError("redirect loop detected")
	}
	
	// Close previous response body
	if resp.Body != nil {
		resp.Body.Close()
	}
	
	// Follow redirect
	req, err := http.NewRequestWithContext(ctx, "GET", redirectURL.String(), nil)
	if err != nil {
		return resp, status, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")
	
	newResp, err := client.Do(req)
	if err != nil {
		return resp, status, err
	}
	
	// Recursively follow redirects
	return followRedirects(ctx, client, newResp, redirectURL.String(), depth+1)
}

// isOKStatus determines if a status code indicates the link is valid.
// 200-299: Success
// 401: Unauthorized (page exists, just requires auth)
// 403: Forbidden (page exists, just requires permissions)
func isOKStatus(status int) bool {
	if status >= 200 && status < 300 {
		return true
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	return false
}

// isRetryableStatus determines if a status code should trigger a retry.
func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || // 408
		status == http.StatusTooManyRequests || // 429
		status >= 500 // 5xx server errors
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}

func isDNSError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such host") || strings.Contains(msg, "server misbehaving")
}

func isRefused(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused")
}

type simpleError string

func (e simpleError) Error() string { return string(e) }
