// fakeext — test double for RUN-001 extension supervision tests.
// A minimal §3-protocol extension: GET /tools, POST /execute, GET /check.
// Reads its listen port from PORT (the RUN-001 convention). Optional test
// hooks via env: FAKEEXT_LOG appends a startup line per process start,
// FAKEEXT_CRASH_AFTER_MS exits after N ms (crash-restart testing).
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if log := os.Getenv("FAKEEXT_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(f, "start pid=%d port=%s ts=%s\n", os.Getpid(), port, time.Now().Format(time.RFC3339Nano))
			f.Close()
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{
			"name":        "fake_echo",
			"description": "echo the message arg back",
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"message"},
			},
		}})
	})
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]string{"result": "echo: " + fmt.Sprint(req.Args["message"])})
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"alert": false, "message": ""})
	})

	if ms := os.Getenv("FAKEEXT_CRASH_AFTER_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil {
			go func() {
				time.Sleep(time.Duration(n) * time.Millisecond)
				os.Exit(3)
			}()
		}
	}
	http.ListenAndServe("127.0.0.1:"+port, mux)
}
