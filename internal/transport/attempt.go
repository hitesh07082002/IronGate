package transport

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
)

const (
	HeaderServedBy    = "X-Served-By"
	HeaderRetryCount  = "X-Retry-Count"
	HeaderRetryTarget = "X-Retry-Target"
)

type attemptContextKey string

const attemptMetadataKey attemptContextKey = "attemptMetadata"

type attemptMetadata struct {
	retryCount   int
	target       string
	triedTargets []string
}

type AttemptError struct {
	Err        error
	RetryCount int
	Target     string
}

func (e *AttemptError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *AttemptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func withAttemptMetadata(req *http.Request, metadata attemptMetadata) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), attemptMetadataKey, metadata))
}

func getAttemptMetadata(req *http.Request) attemptMetadata {
	if req == nil {
		return attemptMetadata{}
	}

	metadata, _ := req.Context().Value(attemptMetadataKey).(attemptMetadata)
	return metadata
}

func (metadata attemptMetadata) withRetryCount(retryCount int) attemptMetadata {
	metadata.retryCount = retryCount
	return metadata
}

func (metadata attemptMetadata) withTarget(target string) attemptMetadata {
	metadata.target = target
	return metadata
}

func (metadata attemptMetadata) withTriedTargets(targets map[string]struct{}) attemptMetadata {
	if len(targets) == 0 {
		metadata.triedTargets = nil
		return metadata
	}

	metadata.triedTargets = metadata.triedTargets[:0]
	for target := range targets {
		metadata.triedTargets = append(metadata.triedTargets, target)
	}
	sort.Strings(metadata.triedTargets)
	return metadata
}

func (metadata attemptMetadata) excludedTargets() map[string]struct{} {
	if len(metadata.triedTargets) == 0 {
		return nil
	}

	excluded := make(map[string]struct{}, len(metadata.triedTargets))
	for _, target := range metadata.triedTargets {
		excluded[target] = struct{}{}
	}
	return excluded
}

func applyAttemptHeaders(headers http.Header, metadata attemptMetadata) {
	if headers == nil {
		return
	}

	headers.Set(HeaderRetryCount, strconv.Itoa(metadata.retryCount))
	if metadata.target != "" {
		headers.Set(HeaderRetryTarget, metadata.target)
	}
}

func metadataFromError(err error) (attemptMetadata, bool) {
	var attemptErr *AttemptError
	if !errors.As(err, &attemptErr) {
		return attemptMetadata{}, false
	}

	return attemptMetadata{
		retryCount: attemptErr.RetryCount,
		target:     attemptErr.Target,
	}, true
}

func ApplyErrorHeaders(headers http.Header, err error) {
	metadata, ok := metadataFromError(err)
	if !ok {
		return
	}

	applyAttemptHeaders(headers, metadata)
}
