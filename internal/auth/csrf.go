package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
)

const (
	CSRFCookieName = "dashboard_csrf"
	CSRFHeaderName = "X-CSRF-Token"
	CSRFFormField  = "csrf_token"
	csrfTokenLen   = 32
)

var ErrCSRFMismatch = errors.New("csrf token missing or mismatched")

// EnsureCSRFCookie sets a fresh CSRF cookie if the request has none. Returns
// the token currently in effect so handlers / templates can echo it back.
// The cookie is non-HttpOnly because the JS layer reads it for the header.
func EnsureCSRFCookie(w http.ResponseWriter, r *http.Request, secureTLS bool) (string, error) {
	if c, err := r.Cookie(CSRFCookieName); err == nil && len(c.Value) == csrfTokenLen*2 {
		return c.Value, nil
	}
	tok, err := randomHex(csrfTokenLen)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: false, // JS reads it for the X-CSRF-Token header
		Secure:   secureTLS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
	return tok, nil
}

// VerifyCSRF implements the double-submit cookie pattern: the token from
// the cookie must equal the token from the header (or form field for
// browser form posts), compared in constant time.
func VerifyCSRF(r *http.Request) error {
	c, err := r.Cookie(CSRFCookieName)
	if err != nil || c.Value == "" {
		return ErrCSRFMismatch
	}
	supplied := r.Header.Get(CSRFHeaderName)
	if supplied == "" {
		supplied = r.PostFormValue(CSRFFormField)
	}
	if supplied == "" {
		return ErrCSRFMismatch
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(supplied)) != 1 {
		return ErrCSRFMismatch
	}
	return nil
}

// hexToken is exposed for tests that need to mint a token without going
// through the cookie-issuing path.
func hexToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
