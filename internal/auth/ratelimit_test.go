package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	for i := 1; i <= 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if ok, retry := l.Allow("1.2.3.4"); ok {
		t.Fatalf("attempt 4 should be blocked")
	} else if retry <= 0 {
		t.Fatalf("retryAfter should be positive, got %v", retry)
	}
	// Other IPs are independent.
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("different IP should be unaffected")
	}
	// Reset clears the bucket.
	l.Reset("1.2.3.4")
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("post-reset attempt should be allowed")
	}
}
