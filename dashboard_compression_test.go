package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardCompressionNegotiation(t *testing.T) {
	cases := []struct {
		name       string
		accept     string
		compressed bool
	}{
		{"compresses gzip clients", "gzip, deflate", true},
		{"compresses wildcard clients", "*", true},
		{"leaves clients without gzip unchanged", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := gzipDashboard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok"}`))
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if tc.compressed {
				if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
					t.Fatalf("content type = %q, want JSON", got)
				}
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
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				if got := string(body); got != `{"status":"ok"}` {
					t.Fatalf("decoded body = %q, want JSON response", got)
				}
				return
			}
			if got := rr.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("content encoding = %q, want empty", got)
			}
			if got := rr.Body.String(); got != `{"status":"ok"}` {
				t.Fatalf("body = %q, want JSON response", got)
			}
		})
	}

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

func TestDashboardCompressionSkipsNonAPIResponses(t *testing.T) {
	handler := gzipDashboard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>ok</html>"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q, want empty", got)
	}
	if got := rr.Body.String(); got != "<html>ok</html>" {
		t.Fatalf("body = %q, want HTML response", got)
	}
}

func TestDashboardCompressionSetsMissingAPIContentType(t *testing.T) {
	handler := gzipDashboard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q, want JSON", got)
	}
}
