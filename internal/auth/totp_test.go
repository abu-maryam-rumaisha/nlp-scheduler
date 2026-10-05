package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors for SHA-1, truncated to 6 digits.
func TestTOTPVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	} {
		now := time.Unix(tc.unix, 0)
		step, ok := CheckTOTP(secret, tc.code, now)
		if !ok || step != totpStep(now) {
			t.Errorf("t=%d code=%s: ok=%v step=%d", tc.unix, tc.code, ok, step)
		}
	}
}

func TestTOTPWindow(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := b32.DecodeString(secret)
	now := time.Unix(1_700_000_000, 0)
	cur := totpStep(now)

	for _, d := range []int64{-1, 0, 1} {
		if step, ok := CheckTOTP(secret, totpCode(key, cur+d), now); !ok || step != cur+d {
			t.Errorf("offset %d rejected", d)
		}
	}
	for _, d := range []int64{-3, -2, 2, 3} {
		if _, ok := CheckTOTP(secret, totpCode(key, cur+d), now); ok {
			t.Errorf("offset %d accepted", d)
		}
	}
	// Spaces are tolerated, wrong lengths are not.
	c := totpCode(key, cur)
	if _, ok := CheckTOTP(secret, c[:3]+" "+c[3:], now); !ok {
		t.Error("code with space rejected")
	}
	if _, ok := CheckTOTP(secret, c[:5], now); ok {
		t.Error("short code accepted")
	}
}

func TestTOTPURL(t *testing.T) {
	u := TOTPURL("ABC", "alice")
	if !strings.HasPrefix(u, "otpauth://totp/OpenResty%20Manager:alice?") || !strings.Contains(u, "secret=ABC") {
		t.Errorf("unexpected URL %q", u)
	}
	if png, err := TOTPQRCode(u); err != nil || !strings.HasPrefix(png, "data:image/png;base64,") {
		t.Errorf("QR code: %v", err)
	}
}
