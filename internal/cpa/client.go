// Package cpa talks to CLIProxyAPI's v8 management API.
// The management key stays here, server-side; the browser never sees it.
package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Client reads the management key from a file and re-reads it when the file changes.
// After CPA rejects the key it stops calling entirely: five bad attempts ban the IP,
// localhost included, for 30 minutes. Fixing the key file clears the stop.
type Client struct {
	base    string
	keyPath string
	http    *http.Client

	mu      sync.Mutex
	key     string
	keyMod  time.Time
	authErr string
}

func New(base, keyPath string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), keyPath: keyPath, http: &http.Client{Timeout: 75 * time.Second}}
}

// APIError carries CPA's status and error body so the UI can show what went wrong.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("cpa %d: %s", e.Status, e.Body) }

var ErrAuth = errors.New("management key rejected")

// AuthProblem reports why calls are currently blocked, or "" when they aren't.
func (c *Client) AuthProblem() string {
	if _, err := c.currentKey(); err != nil {
		return err.Error()
	}
	return ""
}

func (c *Client) currentKey() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(c.keyPath)
	if err != nil {
		return "", fmt.Errorf("no management key at %s", c.keyPath)
	}
	if !info.ModTime().Equal(c.keyMod) {
		raw, err := os.ReadFile(c.keyPath)
		if err != nil {
			return "", fmt.Errorf("read management key: %w", err)
		}
		c.key = strings.TrimSpace(string(raw))
		c.keyMod = info.ModTime()
		c.authErr = ""
	}
	if c.key == "" {
		return "", fmt.Errorf("management key file %s is empty", c.keyPath)
	}
	if c.authErr != "" {
		return "", fmt.Errorf("%w: %s (fix the key file to retry)", ErrAuth, c.authErr)
	}
	return c.key, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	key, err := c.currentKey()
	if err != nil {
		return err
	}
	u := c.base + "/v8/management" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		msg := string(bytes.TrimSpace(raw))
		if res.StatusCode == http.StatusUnauthorized || (res.StatusCode == http.StatusForbidden && strings.Contains(msg, "banned")) {
			c.mu.Lock()
			c.authErr = msg
			c.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrAuth, msg)
		}
		return &APIError{Status: res.StatusCode, Body: msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
