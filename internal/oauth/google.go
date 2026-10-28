package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

const (
	GoogleClientID     = "32555940559.apps.googleusercontent.com"
	GoogleClientSecret = "ZmssLNjJy2998hD4CTdg2xqq"
	GoogleAuthEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenEndpoint = "https://oauth2.googleapis.com/token"
	GoogleUserinfoURL  = "https://www.googleapis.com/oauth2/v3/userinfo"
	GoogleScope        = "https://www.googleapis.com/auth/generative-language https://www.googleapis.com/auth/cloud-platform email profile"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

type UserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

type LoginSession struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	active   bool
}

var (
	currentSession   *LoginSession
	currentSessionMu sync.Mutex
)

// StartGoogleLogin starts a local loopback HTTP server, generates the OAuth URL,
// opens the default web browser, and invokes onDone once the user signs in or cancels.
func StartGoogleLogin(ctx context.Context, onDone func(token *TokenResponse, email string, err error)) error {
	currentSessionMu.Lock()
	if currentSession != nil && currentSession.active {
		_ = currentSession.stop()
	}
	s := &LoginSession{active: true}
	currentSession = s
	currentSessionMu.Unlock()

	// Try default port 8765 first, else ephemeral 0
	var ln net.Listener
	var err error
	ln, err = net.Listen("tcp", "127.0.0.1:8765")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("не вдалося запустити локальний слухач: %w", err)
		}
	}
	s.listener = ln
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := q.Get("error"); errStr != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, errorHTML, "Авторизацію скасовано: "+errStr)
			go func() {
				_ = s.stop()
				onDone(nil, "", fmt.Errorf("Google OAuth: %s", errStr))
			}()
			return
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, errorHTML, "Відсутній код авторизації")
			go func() {
				_ = s.stop()
				onDone(nil, "", errors.New("Google OAuth: порожній код"))
			}()
			return
		}

		// Exchange code for token
		tCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		token, err := ExchangeGoogleCode(tCtx, code, redirectURI)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, errorHTML, "Не вдалося отримати токен: "+err.Error())
			go func() {
				_ = s.stop()
				onDone(nil, "", err)
			}()
			return
		}

		// Fetch user info for nice display in UI
		email := ""
		if uinfo, uerr := FetchUserInfo(tCtx, token.AccessToken); uerr == nil && uinfo != nil {
			email = uinfo.Email
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, successHTML, email)

		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = s.stop()
			onDone(token, email, nil)
		}()
	})

	s.server = &http.Server{Handler: mux}

	go func() {
		_ = s.server.Serve(ln)
	}()

	// Open browser
	authURL := BuildAuthURL(redirectURI)
	if oerr := sysx.OpenPath(authURL); oerr != nil {
		_ = s.stop()
		return fmt.Errorf("не вдалося відкрити браузер: %w", oerr)
	}

	// Auto-cancel after 3 minutes if no response
	go func() {
		select {
		case <-time.After(3 * time.Minute):
			currentSessionMu.Lock()
			if currentSession == s && s.active {
				_ = s.stop()
				onDone(nil, "", errors.New("час очікування авторизації вичерпано (3 хв)"))
			}
			currentSessionMu.Unlock()
		case <-ctx.Done():
			_ = s.stop()
		}
	}()

	return nil
}

func (s *LoginSession) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil
	}
	s.active = false
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	return nil
}

// BuildAuthURL creates the Google OAuth 2.0 authorization URL.
func BuildAuthURL(redirectURI string) string {
	v := url.Values{}
	v.Set("client_id", GoogleClientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("response_type", "code")
	v.Set("scope", GoogleScope)
	v.Set("access_type", "offline")
	v.Set("prompt", "consent")
	return GoogleAuthEndpoint + "?" + v.Encode()
}

// ExchangeGoogleCode exchanges the authorization code for access and refresh tokens.
func ExchangeGoogleCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	v := url.Values{}
	v.Set("client_id", GoogleClientID)
	v.Set("client_secret", GoogleClientSecret)
	v.Set("code", code)
	v.Set("grant_type", "authorization_code")
	v.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleTokenEndpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google OAuth error (%d): %s", resp.StatusCode, string(body))
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// RefreshGoogleToken requests a fresh access token using a refresh token.
func RefreshGoogleToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	if refreshToken == "" {
		return nil, errors.New("порожній refresh token")
	}
	v := url.Values{}
	v.Set("client_id", GoogleClientID)
	v.Set("client_secret", GoogleClientSecret)
	v.Set("refresh_token", refreshToken)
	v.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleTokenEndpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google OAuth refresh error (%d): %s", resp.StatusCode, string(body))
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken // retain existing refresh token
	}
	return &tr, nil
}

// FetchUserInfo retrieves user's profile info (email) from Google.
func FetchUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GoogleUserinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Userinfo error (%d): %s", resp.StatusCode, string(body))
	}

	var ui UserInfo
	if err := json.Unmarshal(body, &ui); err != nil {
		return nil, err
	}
	return &ui, nil
}

const successHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>YT Cartell Studio — Авторизація</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f1117; color: #f1f5f9; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; padding: 20px; box-sizing: border-box; }
  .card { background: #181b26; border: 1px solid #2e3447; padding: 48px; border-radius: 20px; text-align: center; max-width: 500px; box-shadow: 0 16px 48px rgba(0,0,0,0.5); }
  .icon { width: 64px; height: 64px; background: rgba(34, 197, 94, 0.15); border-radius: 50%%; display: inline-flex; align-items: center; justify-content: center; margin-bottom: 24px; color: #22c55e; font-size: 32px; }
  h1 { color: #f8fafc; font-size: 24px; margin: 0 0 12px 0; font-weight: 600; }
  p { color: #94a3b8; font-size: 16px; line-height: 1.6; margin: 0 0 24px 0; }
  .email { background: #232838; padding: 8px 16px; border-radius: 8px; color: #38bdf8; font-weight: 500; font-size: 15px; display: inline-block; margin-bottom: 24px; }
  .hint { font-size: 14px; color: #64748b; margin-top: 20px; }
</style>
</head>
<body>
  <div class="card">
    <div class="icon">✓</div>
    <h1>Авторизація успішна!</h1>
    <div class="email">%s</div>
    <p>Ваш акаунт Google успішно підключено до <b>YT Cartell Studio</b>.<br>Тепер ви можете користуватися всіма функціями Gemini та генерацією відео.</p>
    <div class="hint">Цю вкладку можна безпечно закрити. Поверніться до вікна програми.</div>
  </div>
</body>
</html>`

const errorHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>YT Cartell Studio — Помилка</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f1117; color: #f1f5f9; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; padding: 20px; box-sizing: border-box; }
  .card { background: #181b26; border: 1px solid #ef4444; padding: 48px; border-radius: 20px; text-align: center; max-width: 500px; box-shadow: 0 16px 48px rgba(0,0,0,0.5); }
  .icon { width: 64px; height: 64px; background: rgba(239, 68, 68, 0.15); border-radius: 50%%; display: inline-flex; align-items: center; justify-content: center; margin-bottom: 24px; color: #ef4444; font-size: 32px; }
  h1 { color: #f8fafc; font-size: 24px; margin: 0 0 12px 0; }
  p { color: #fca5a5; font-size: 16px; line-height: 1.6; }
</style>
</head>
<body>
  <div class="card">
    <div class="icon">✕</div>
    <h1>Помилка підключення</h1>
    <p>%s</p>
  </div>
</body>
</html>`
