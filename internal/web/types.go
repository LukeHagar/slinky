package web

import "time"

type Result struct {
	URL         string
	OK          bool
	Status      int
	Err         error
	ErrMsg      string
	Depth       int
	CacheHit    bool
	Method      string
	ContentType string
	Sources     []string
}

type Stats struct {
	Pending   int
	Processed int
}

type Config struct {
	MaxDepth       int
	MaxConcurrency int
	RequestTimeout time.Duration
	MaxRetries429  int
	Exclude        []string
}
