package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ctlUsage = "usage: lastcall ctl [-url URL] list | pause <match> <duration|5h-reset|week-reset> | disable <match> | enable <match>"

// ctlAccount holds the fields of /api/snapshot that ctl reads. Its JSON tags also shape `list` output.
type ctlAccount struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Plan     string `json:"plan"`
	Status   string `json:"status"`
	Disabled bool   `json:"disabled"`
	Pause    *struct {
		ResumeAt time.Time `json:"resume_at"`
	} `json:"pause,omitempty"`
}

type ctlRef struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type pauseRequest struct {
	Minutes int    `json:"minutes,omitempty"`
	Until   string `json:"until,omitempty"`
}

type ctlActionResult struct {
	OK       bool       `json:"ok"`
	Action   string     `json:"action"`
	Account  ctlRef     `json:"account"`
	ResumeAt *time.Time `json:"resume_at,omitempty"`
}

type ctlError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type ctlClient struct {
	base   string
	client *http.Client
}

// runCtl runs `lastcall ctl` and returns the exit code. It always writes one JSON object to stdout.
func runCtl(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("ctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	base := fs.String("url", "http://127.0.0.1:8318", "lastcall base URL")
	if err := fs.Parse(args); err != nil {
		return ctlFail(stdout, err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return ctlFail(stdout, errors.New(ctlUsage))
	}
	c := ctlClient{base: strings.TrimRight(*base, "/"), client: &http.Client{Timeout: 30 * time.Second}}
	cmd, operands := rest[0], rest[1:]

	switch cmd {
	case "list":
		if len(operands) != 0 {
			return ctlFail(stdout, errors.New(ctlUsage))
		}
		var snap struct {
			Accounts []ctlAccount `json:"accounts"`
		}
		if err := c.do(http.MethodGet, "/api/snapshot", nil, &snap); err != nil {
			return ctlFail(stdout, err)
		}
		accounts := snap.Accounts
		if accounts == nil {
			accounts = []ctlAccount{}
		}
		return ctlEmit(stdout, accounts)

	case "pause":
		if len(operands) != 2 {
			return ctlFail(stdout, errors.New(ctlUsage))
		}
		req, err := parsePause(operands[1])
		if err != nil {
			return ctlFail(stdout, err)
		}
		acct, err := c.resolve(operands[0])
		if err != nil {
			return ctlFail(stdout, err)
		}
		var res struct {
			ResumeAt time.Time `json:"resume_at"`
		}
		if err := c.do(http.MethodPost, accountPath(acct.Name, cmd), req, &res); err != nil {
			return ctlFail(stdout, err)
		}
		return ctlEmit(stdout, ctlActionResult{OK: true, Action: cmd, Account: refOf(acct), ResumeAt: &res.ResumeAt})

	case "disable", "enable":
		if len(operands) != 1 {
			return ctlFail(stdout, errors.New(ctlUsage))
		}
		acct, err := c.resolve(operands[0])
		if err != nil {
			return ctlFail(stdout, err)
		}
		if err := c.do(http.MethodPost, accountPath(acct.Name, cmd), nil, nil); err != nil {
			return ctlFail(stdout, err)
		}
		return ctlEmit(stdout, ctlActionResult{OK: true, Action: cmd, Account: refOf(acct)})
	}
	return ctlFail(stdout, fmt.Errorf("unknown command %q; %s", cmd, ctlUsage))
}

// parsePause turns "3h", "90m" or "5h-reset" into the body the pause endpoint expects.
// Durations round up to whole minutes.
func parsePause(spec string) (pauseRequest, error) {
	switch spec {
	case "5h-reset":
		return pauseRequest{Until: "5h"}, nil
	case "week-reset":
		return pauseRequest{Until: "week"}, nil
	}
	d, err := time.ParseDuration(spec)
	if err != nil {
		return pauseRequest{}, fmt.Errorf("bad duration %q: use 90m, 3h, 5h-reset or week-reset", spec)
	}
	if d <= 0 {
		return pauseRequest{}, fmt.Errorf("duration %q must be positive", spec)
	}
	minutes := d / time.Minute
	if d%time.Minute != 0 {
		minutes++
	}
	return pauseRequest{Minutes: int(minutes)}, nil
}

// resolve finds the one account that match names. It never picks among several.
func (c ctlClient) resolve(match string) (ctlAccount, error) {
	var snap struct {
		Accounts []ctlAccount `json:"accounts"`
	}
	if err := c.do(http.MethodGet, "/api/snapshot", nil, &snap); err != nil {
		return ctlAccount{}, err
	}
	return resolveAccount(snap.Accounts, match)
}

func resolveAccount(accounts []ctlAccount, match string) (ctlAccount, error) {
	match = strings.TrimSpace(match)
	if match == "" {
		return ctlAccount{}, errors.New("account match is empty; available: " + labelList(accounts))
	}

	var exact []ctlAccount
	for _, a := range accounts {
		if strings.EqualFold(a.Name, match) || strings.EqualFold(a.Label, match) {
			exact = append(exact, a)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}

	lower := strings.ToLower(match)
	var hits []ctlAccount
	for _, a := range accounts {
		if strings.Contains(strings.ToLower(a.Label), lower) ||
			strings.Contains(strings.ToLower(a.Name), lower) ||
			strings.EqualFold(a.Provider, match) {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 0:
		return ctlAccount{}, fmt.Errorf("no account matches %q; available: %s", match, labelList(accounts))
	case 1:
		return hits[0], nil
	default:
		return ctlAccount{}, fmt.Errorf("%q matches %d accounts: %s; use an exact name", match, len(hits), labelList(hits))
	}
}

func labelList(accounts []ctlAccount) string {
	if len(accounts) == 0 {
		return "none"
	}
	labels := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if a.Label != "" {
			labels = append(labels, a.Label+" ("+a.Name+")")
		} else {
			labels = append(labels, a.Name)
		}
	}
	return strings.Join(labels, ", ")
}

func refOf(a ctlAccount) ctlRef {
	return ctlRef{Name: a.Name, Label: a.Label}
}

func accountPath(name, action string) string {
	return "/api/accounts/" + url.PathEscape(name) + "/" + action
}

// do sends one request to lastcall. A nil body sends no body. A nil out skips decoding.
func (c ctlClient) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Lastcall", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("lastcall is not reachable at %s", c.base)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		return fmt.Errorf("lastcall returned %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func ctlEmit(stdout io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

func ctlFail(stdout io.Writer, err error) int {
	ctlEmit(stdout, ctlError{OK: false, Error: err.Error()})
	return 1
}
