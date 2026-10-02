// lastcall: a usage page and account switchboard for CLIProxyAPI.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/opencode"
	"lastcall/internal/server"
	"lastcall/internal/state"
	"lastcall/web"
)

func main() {
	home, _ := os.UserHomeDir()
	listen := flag.String("listen", "127.0.0.1:8318", "address to serve the UI on (keep it loopback)")
	cpaURL := flag.String("cpa", "http://127.0.0.1:8317", "CLIProxyAPI base URL")
	keyPath := flag.String("key", filepath.Join(home, ".config/lastcall/management-key"), "file holding the CPA management key")
	statePath := flag.String("state", filepath.Join(home, "Library/Application Support/lastcall/state.json"), "lastcall state file")
	quotaEvery := flag.Duration("quota-every", 5*time.Minute, "how often to poll provider quota")
	ocDB := flag.String("opencode-db", filepath.Join(home, ".local/share/opencode/opencode.db"), "opencode database (empty to skip)")
	ocPlan := flag.String("opencode-plan", "go", "OpenCode Go plan: go | go-plus")
	flag.Parse()

	if host, _, err := net.SplitHostPort(*listen); err == nil {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			log.Printf("warning: %s is not loopback; anyone who can reach it can disable your accounts", *listen)
		}
	}

	st, err := state.Open(*statePath)
	if err != nil {
		log.Fatalf("state: %v", err)
	}
	var oc *opencode.Reader
	if _, err := os.Stat(*ocDB); *ocDB != "" && err == nil {
		oc = &opencode.Reader{DB: *ocDB, Plan: *ocPlan}
	}
	srv := server.New(*cpaURL, cpa.New(*cpaURL, *keyPath), st, *quotaEvery, oc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Run(ctx)

	hs := &http.Server{Addr: *listen, Handler: srv.Handler(web.FS), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut)
	}()
	log.Printf("lastcall on http://%s (CPA %s)", *listen, *cpaURL)
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
