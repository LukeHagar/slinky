package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync/atomic"
	"time"
)

// CheckURLs performs concurrent GET requests for each URL and emits Result events.
// sources maps URL -> list of file paths where it was found.
func CheckURLs(ctx context.Context, urls []string, sources map[string][]string, out chan<- Result, stats chan<- Stats, cfg Config) {
	defer close(out)

	// Build HTTP client with optimized connection pooling
	// Increase MaxIdleConns to handle many unique domains efficiently
	maxIdleConns := cfg.MaxConcurrency * 4
	if maxIdleConns < 100 {
		maxIdleConns = 100 // Minimum 100 idle connections for better performance across domains
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxConcurrency,
		MaxConnsPerHost:       cfg.MaxConcurrency,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: cfg.RequestTimeout,
	}
	client := &http.Client{Timeout: cfg.RequestTimeout, Transport: transport}

	type job struct{ url string }
	jobs := make(chan job, len(urls))
	done := make(chan struct{})

	// Use atomic counters to avoid race conditions
	var processed int64
	var pending int64
	var jobCount int64

	// Seed jobs (URLs are already deduplicated in check.go, so no need to deduplicate here)
	for _, u := range urls {
		if u == "" {
			continue
		}
		jobs <- job{url: u}
		jobCount++
	}
	close(jobs)

	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 8
	}
	// Set pending to actual number of jobs enqueued
	pending = jobCount

	worker := func() {
		for j := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
			}
			
			var ok bool
			var status int
			var err error
			var cacheHit bool
			
			// Check cache first if available
			if cfg.Cache != nil {
				if cached, found := cfg.Cache.Get(j.url); found {
					ok = cached.OK
					status = cached.Status
					err = nil
					if cached.ErrMsg != "" {
						err = fmt.Errorf("%s", cached.ErrMsg)
					}
					cacheHit = true
				}
			}
			
			// If not cached, fetch from network
			if !cacheHit {
				var resp *http.Response
				ok, status, resp, err = fetchWithMethod(ctx, client, http.MethodGet, j.url)
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				// Status code handling is now done in fetchWithMethod:
				// - 200-299, 401, 403 are OK (page exists)
				// - 404, DNS errors, connection refused are bad (flagged)
				// - 408, 429, 5xx are retried then flagged if still failing
				
				// Store in cache
				if cfg.Cache != nil {
					errMsg := ""
					if err != nil {
						errMsg = err.Error()
					}
					cfg.Cache.Set(j.url, ok, status, errMsg)
				}
			}
			
			// Check context before sending result
			select {
			case <-ctx.Done():
				return
			default:
			}

			var srcs []string
			if sources != nil {
				srcs = sources[j.url]
			}

			// Send result with context check
			select {
			case out <- Result{URL: j.url, OK: ok, Status: status, Err: err, ErrMsg: errString(err), Method: http.MethodGet, Sources: cloneAndSort(srcs), CacheHit: cacheHit}:
			case <-ctx.Done():
				return
			}

			// Atomically update counters
			proc := atomic.AddInt64(&processed, 1)
			pend := atomic.AddInt64(&pending, -1)
			if stats != nil {
				select {
				case stats <- Stats{Pending: int(pend), Processed: int(proc)}:
				default:
				}
			}
		}
		done <- struct{}{}
	}

	for i := 0; i < concurrency; i++ {
		go worker()
	}
	for i := 0; i < concurrency; i++ {
		<-done
	}
}

func cloneAndSort(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
