package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyCSRFRejectsMissing(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	if err := VerifyCSRF(r); err != ErrCSRFMismatch {
		t.Fatalf("expected ErrCSRFMismatch, got %v", err)
	}
}

func TestVerifyCSRFRejectsMismatch(t *testing.T) {
	tok, _ := hexToken(32)
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})
	r.Header.Set(CSRFHeaderName, "not-the-right-token")
	if err := VerifyCSRF(r); err != ErrCSRFMismatch {
		t.Fatalf("expected ErrCSRFMismatch, got %v", err)
	}
}

func TestVerifyCSRFAcceptsMatch(t *testing.T) {
	tok, _ := hexToken(32)
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})
	r.Header.Set(CSRFHeaderName, tok)
	if err := VerifyCSRF(r); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestVerifyCSRFAcceptsFormField(t *testing.T) {
	tok, _ := hexToken(32)
	body := CSRFFormField + "=" + tok
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})
	if err := VerifyCSRF(r); err != nil {
		t.Fatalf("expected nil for form-field match, got %v", err)
	}
}
