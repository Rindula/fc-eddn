package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var version = "0.0.0-dev"
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type config struct {
	clientID, redirect, audience, commander, stateDir, mode string
	interval                                                time.Duration
}

func (c config) path(name string) string {
	if name == "token.json" {
		return filepath.Join(c.stateDir, name)
	}
	return filepath.Join(c.stateDir, c.mode+"-"+name)
}

func env(key, fallback string) string {
	if value := os.Getenv("FC_EDDN_" + key); value != "" {
		return value
	}
	return fallback
}

type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

func request(req *http.Request, limit int64, dst any) ([]byte, error) {
	timeout := 45 * time.Second
	if req.URL.Path == "/fleetcarrier" {
		timeout = 22 * time.Minute
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("User-Agent", "fc-eddn/"+version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("HTTP response exceeded size limit")
	}
	if dst != nil {
		if err := json.Unmarshal(data, dst); err != nil {
			return nil, errors.New("invalid JSON in HTTP response")
		}
	}
	return data, nil
}

func execute(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("Usage: fc-eddn login | sync [--once] | version")
		return nil
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] != "login" && args[0] != "sync" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	c := config{
		clientID: env("CLIENT_ID", ""), redirect: env("REDIRECT_URI", "http://127.0.0.1:38473/callback"),
		audience: env("AUDIENCE", "frontier"), commander: env("COMMANDER", ""),
		stateDir: env("STATE_DIR", ".fc-eddn"), mode: env("MODE", "dry-run"),
	}
	if c.clientID == "" {
		return errors.New("set FC_EDDN_CLIENT_ID to your Frontier OAuth client ID")
	}
	if args[0] == "login" {
		if len(args) != 1 {
			return errors.New("usage: fc-eddn login")
		}
		return login(ctx, c)
	}
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	once := flags.Bool("once", false, "run a single synchronization")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if c.mode != "live" && c.mode != "test" && c.mode != "dry-run" {
		return errors.New("FC_EDDN_MODE must be dry-run, test or live")
	}
	if strings.TrimSpace(c.commander) == "" {
		return errors.New("set FC_EDDN_COMMANDER to the in-game Commander name")
	}
	interval, err := time.ParseDuration(env("INTERVAL", "15m"))
	if err != nil || interval < 15*time.Minute {
		return errors.New("FC_EDDN_INTERVAL must be at least 15m")
	}
	c.interval = interval
	s := syncer{config: c, sent: map[string]string{}}
	if err := load(c.path("token.json"), &s.token); err != nil {
		return fmt.Errorf("run login first: %w", err)
	}
	if err := load(c.path("sent.json"), &s.sent); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.sent == nil {
		s.sent = map[string]string{}
	}
	for {
		err := s.sync(ctx)
		if *once {
			return err
		}
		if err != nil && ctx.Err() == nil {
			log.Print(err)
		}
		timer := time.NewTimer(c.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) && ctx.Err() == nil {
		log.Fatal(err)
	}
}
