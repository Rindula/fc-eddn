package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const authBase = "https://auth.frontierstore.net"

type token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Type    string    `json:"token_type"`
	Expires int64     `json:"expires_in"`
	Expiry  time.Time `json:"expiry"`
}

func save(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func load(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid state file %s", filepath.Base(path))
	}
	return nil
}

func exchange(ctx context.Context, c config, form url.Values) (token, error) {
	form.Set("client_id", c.clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authBase+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var t token
	if _, err := request(req, 1<<20, &t); err != nil {
		return t, fmt.Errorf("Frontier token exchange: %w", err)
	}
	if t.Refresh == "" {
		t.Refresh = form.Get("refresh_token")
	}
	if t.Access == "" || t.Refresh == "" || t.Expires <= 0 || !strings.EqualFold(t.Type, "Bearer") {
		return t, errors.New("incomplete Frontier token response")
	}
	t.Expiry = time.Now().Add(time.Duration(t.Expires) * time.Second)
	return t, save(c.path("token.json"), t)
}

func login(ctx context.Context, c config) error {
	redirect, err := url.Parse(c.redirect)
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Port() == "" ||
		redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" || redirect.Path == "" {
		return errors.New("FC_EDDN_REDIRECT_URI must be an HTTP URL with 127.0.0.1, port and callback path")
	}
	ln, err := net.Listen("tcp4", redirect.Host)
	if err != nil {
		return err
	}
	defer ln.Close()
	random := func() string { return base64.RawURLEncoding.EncodeToString(randBytes()) }
	state, verifier := random(), random()
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"audience": {c.audience}, "scope": {"auth capi"}, "response_type": {"code"},
		"client_id": {c.clientID}, "redirect_uri": {c.redirect}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"},
	}
	codes := make(chan string, 1)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != redirect.Path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintln(w, "Authorization response received. Return to the terminal.")
		select {
		case codes <- q.Get("code"):
		default:
		}
	})}
	defer srv.Close()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- srv.Serve(ln) }()
	fmt.Println("Open in your browser:", authBase+"/auth?"+query.Encode())
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case code := <-codes:
		if code == "" {
			return errors.New("Frontier authorization declined or failed")
		}
		_, err = exchange(ctx, c, url.Values{"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {c.redirect}, "code_verifier": {verifier}})
		return err
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randBytes() []byte {
	data := make([]byte, 32)
	rand.Read(data)
	return data
}

func (s *syncer) capi(ctx context.Context, endpoint string, max int64, dst any) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if time.Until(s.token.Expiry) < time.Minute {
			t, err := exchange(ctx, s.config, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {s.token.Refresh}})
			if err != nil {
				return nil, err
			}
			s.token = t
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://companion.orerve.net"+endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.token.Access)
		data, err := request(req, max, dst)
		var response statusError
		if attempt == 0 && errors.As(err, &response) && (response.code == 401 || response.code == 422) {
			s.token.Expiry = time.Time{}
			continue
		}
		return data, err
	}
	return nil, errors.New("Frontier authorization failed")
}
