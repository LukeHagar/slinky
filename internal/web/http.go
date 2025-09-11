package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0 Safari/537.36"

func fetchWithMethod(ctx context.Context, client *http.Client, method string, raw string) (bool, int, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		return false, 0, nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		if isDNSError(err) {
			return false, 404, nil, simpleError("host not found")
		}
		if isTimeout(err) {
			return false, 408, nil, simpleError("request timeout")
		}
		if isRefused(err) {
			return false, 503, nil, simpleError("connection refused")
		}
		return false, 0, nil, err
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 400, resp.StatusCode, resp, nil
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
