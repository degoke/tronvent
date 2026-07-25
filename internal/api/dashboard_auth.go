package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "tronvent_dashboard"
	csrfCookieName    = "tronvent_csrf"
	sessionMaxAge     = 24 * time.Hour
)

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) signSessionPayload(payload string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.AdminAPIToken))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) createSessionToken() string {
	exp := time.Now().Add(sessionMaxAge).Unix()
	payload := strconv.FormatInt(exp, 10)
	return payload + "." + s.signSessionPayload(payload)
}

func (s *Server) validateSessionToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	if s.signSessionPayload(parts[0]) != parts[1] {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.createSessionToken(),
		Path:     "/dashboard",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isSecureRequest(r),
		MaxAge:   int(sessionMaxAge.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/dashboard",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isSecureRequest(r),
		MaxAge:   -1,
	})
}

func (s *Server) hasValidSession(r *http.Request) bool {
	if s.cfg.AdminAPIToken == "" {
		return false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	return s.validateSessionToken(cookie.Value)
}

func (s *Server) randomCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *Server) ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token := s.randomCSRFToken()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/dashboard",
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Secure:   isSecureRequest(r),
		MaxAge:   int(sessionMaxAge.Seconds()),
	})
	return token
}

func (s *Server) requireDashboardSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminAPIToken == "" {
			http.Error(w, "dashboard not configured", http.StatusServiceUnavailable)
			return
		}
		if !s.hasValidSession(r) {
			http.Redirect(w, r, "/dashboard/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireDashboardCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, "missing CSRF cookie", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-CSRF-Token") != cookie.Value {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
