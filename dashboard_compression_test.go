package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardCompressionNegotiation(t *testing.T) {
	handler := gzipDashboard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))

	t.Run("compresses gzip clients", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("content encoding = %q, want gzip", got)
		}
		if got := rr.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Fatalf("vary = %q, want Accept-Encoding", got)
		}

		reader, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		reader.Close()
		if got := string(body); got != `{"status":"ok"}` {
			t.Fatalf("decoded body = %q, want JSON response", got)
		}
	})

	t.Run("leaves clients without gzip unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("content encoding = %q, want empty", got)
		}
		if got := rr.Body.String(); got != `{"status":"ok"}` {
			t.Fatalf("body = %q, want JSON response", got)
		}
	})

	t.Run("does not compress event streams", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		handler := gzipDashboard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("data: ready\n\n"))
		}))
		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("content encoding = %q, want empty", got)
		}
		if got := rr.Body.String(); got != "data: ready\n\n" {
			t.Fatalf("body = %q, want event stream", got)
		}
	})
}
