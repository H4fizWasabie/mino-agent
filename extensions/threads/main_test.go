package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeMeta(t *testing.T, handler func(path string, n int) (int, string)) string {
	t.Helper()
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		code, body := handler(r.URL.Path, n)
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func tok() tokenData {
	return tokenData{AccessToken: "tok", ThreadsUserID: "u1", ExpiresAt: time.Now().Add(time.Hour)}
}

func TestThreadsPostRetriesOnceWhenMetaCannotSeeContainer(t *testing.T) {
	graphBase = fakeMeta(t, func(path string, n int) (int, string) {
		switch path {
		case "/v1.0/u1/threads":
			return 200, `{"id":"c1"}`
		case "/v1.0/u1/threads_publish":
			if n <= 3 { // 1st publish fails; 2nd succeeds
				return 200, `{"error":{"message":"The requested resource does not exist"}}`
			}
			return 200, `{"id":"post1"}`
		}
		return 404, `{}`
	})
	retryDelay = time.Millisecond

	res := threadsPost(tok(), map[string]any{"text": "hello"})
	if !strings.Contains(res, "post1") {
		t.Fatalf("expected success after one retry, got: %s", res)
	}
}

func TestThreadsPostFailsOnMissingContainerAfterRetry(t *testing.T) {
	graphBase = fakeMeta(t, func(path string, n int) (int, string) {
		if path == "/v1.0/u1/threads" {
			return 200, `{"id":"c1"}`
		}
		return 200, `{"error":{"message":"The requested resource does not exist"}}`
	})
	retryDelay = time.Millisecond

	res := threadsPost(tok(), map[string]any{"text": "hello"})
	if !strings.Contains(res, "The requested resource does not exist") {
		t.Fatalf("expected original error surfaced after retry, got: %s", res)
	}
}

func TestThreadsPostNeverRetriesRealErrors(t *testing.T) {
	var publishes int
	graphBase = fakeMeta(t, func(path string, n int) (int, string) {
		if path == "/v1.0/u1/threads" {
			return 200, `{"id":"c1"}`
		}
		publishes++
		return 200, `{"error":{"message":"User rate limit reached"}}`
	})
	retryDelay = time.Millisecond

	res := threadsPost(tok(), map[string]any{"text": "hello"})
	if !strings.Contains(res, "User rate limit reached") {
		t.Fatalf("expected original error, got: %s", res)
	}
	if publishes != 1 {
		t.Fatalf("expected no retry on non-race error, publishes=%d", publishes)
	}
}

func TestCreateMediaContainerRejectsBadResponses(t *testing.T) {
	graphBase = fakeMeta(t, func(path string, n int) (int, string) {
		if n == 1 {
			return 200, `{}` // no id, no error
		}
		return 429, `rate limited` // non-200
	})
	retryDelay = time.Millisecond

	if res := createMediaContainer(tok(), "TEXT", "hi", "", ""); !strings.Contains(res, "empty container id") {
		t.Fatalf("expected empty-id guard, got: %s", res)
	}
	if res := createMediaContainer(tok(), "TEXT", "hi", "", ""); !strings.Contains(res, "HTTP 429") {
		t.Fatalf("expected HTTP status guard, got: %s", res)
	}
}
