package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testAccounts = []ctlAccount{
	{Name: "claude-a.json", Provider: "claude", Label: "a@example.com"},
	{Name: "claude-b.json", Provider: "claude", Label: "b@example.com"},
	{Name: "antigravity-foo@gmail.com.json", Provider: "antigravity", Label: "foo@gmail.com"},
}

func TestResolveAccount(t *testing.T) {
	tests := []struct {
		name    string
		match   string
		want    string
		wantErr string
	}{
		{name: "exact name", match: "antigravity-foo@gmail.com.json", want: "antigravity-foo@gmail.com.json"},
		{name: "exact label, any case", match: "FOO@gmail.com", want: "antigravity-foo@gmail.com.json"},
		{name: "label substring", match: "b@example", want: "claude-b.json"},
		{name: "provider", match: "antigravity", want: "antigravity-foo@gmail.com.json"},
		{name: "ambiguous provider", match: "claude", wantErr: "matches 2 accounts"},
		{name: "no match lists labels", match: "nomatch", wantErr: "available: a@example.com (claude-a.json)"},
		{name: "empty match", match: "  ", wantErr: "match is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveAccount(testAccounts, tt.match)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.want {
				t.Fatalf("got %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestParsePause(t *testing.T) {
	tests := map[string]pauseRequest{
		"3h":         {Minutes: 180},
		"90m":        {Minutes: 90},
		"1s":         {Minutes: 1},
		"90s":        {Minutes: 2},
		"5h-reset":   {Until: "5h"},
		"week-reset": {Until: "week"},
	}
	for spec, want := range tests {
		got, err := parsePause(spec)
		if err != nil || got != want {
			t.Fatalf("parsePause(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
	}
	if _, err := parsePause("soon"); err == nil {
		t.Fatal("parsePause(soon) should fail")
	}
	if _, err := parsePause("-1h"); err == nil {
		t.Fatal("parsePause(-1h) should fail")
	}
}

func TestCtlPauseAndDisable(t *testing.T) {
	var calls []string
	var pauseBody struct {
		Minutes int `json:"minutes"`
	}
	var pauseHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/snapshot":
			json.NewEncoder(w).Encode(map[string]any{"accounts": testAccounts})
		case "/api/accounts/claude-b.json/pause":
			pauseHeader = r.Header.Get("X-Lastcall")
			json.NewDecoder(r.Body).Decode(&pauseBody)
			w.Write([]byte(`{"resume_at":"2026-11-12T10:00:00+05:30"}`))
		case "/api/accounts/claude-b.json/disable":
			w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"no such account"}`))
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	if code := runCtl([]string{"-url", srv.URL, "pause", "b@example", "3h"}, &out); code != 0 {
		t.Fatalf("pause exit %d: %s", code, out.String())
	}
	if pauseBody.Minutes != 180 || pauseHeader != "1" {
		t.Fatalf("pause sent minutes=%d header=%q", pauseBody.Minutes, pauseHeader)
	}
	var res struct {
		OK      bool   `json:"ok"`
		Action  string `json:"action"`
		Account struct {
			Name string `json:"name"`
		} `json:"account"`
		ResumeAt time.Time `json:"resume_at"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("pause output not JSON: %v: %s", err, out.String())
	}
	if !res.OK || res.Action != "pause" || res.Account.Name != "claude-b.json" || res.ResumeAt.IsZero() {
		t.Fatalf("unexpected pause output: %s", out.String())
	}

	out.Reset()
	if code := runCtl([]string{"-url", srv.URL, "disable", "claude-b.json"}, &out); code != 0 {
		t.Fatalf("disable exit %d: %s", code, out.String())
	}
	if last := calls[len(calls)-1]; last != "POST /api/accounts/claude-b.json/disable" {
		t.Fatalf("last call = %q", last)
	}
}

func TestCtlAmbiguousMatchExitsOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"accounts": testAccounts})
	}))
	defer srv.Close()

	var out bytes.Buffer
	if code := runCtl([]string{"-url", srv.URL, "disable", "claude"}, &out); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.OK || !strings.Contains(res.Error, "matches 2 accounts") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCtlListFailsWhenSnapshotHasErrorAndNoAccounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accounts":[],"error":"management key missing"}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	if code := runCtl([]string{"-url", srv.URL, "list"}, &out); code != 1 {
		t.Fatalf("exit = %d, want 1; out = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "management key missing") {
		t.Fatalf("output %s does not carry the server error", out.String())
	}
}
