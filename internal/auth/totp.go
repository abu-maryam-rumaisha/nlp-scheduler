package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

// TOTP parameters (RFC 6238). These are the defaults Google Authenticator
// and every other authenticator app assume, so they are not configurable.
const (
	totpPeriod = 30 * time.Second
	totpDigits = 6
	// Codes from one step before or after now are accepted, to allow for
	// clock drift and for typing a code just as it rolls over.
	totpSkew = 1

	TOTPIssuer = "OpenResty Manager"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret, base32 encoded as
// authenticator apps expect.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// TOTPURL is the otpauth:// URL an authenticator app imports from a QR code.
func TOTPURL(secret, account string) string {
	label := url.PathEscape(TOTPIssuer + ":" + account)
	q := url.Values{
		"secret":    {secret},
		"issuer":    {TOTPIssuer},
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprint(totpDigits)},
		"period":    {fmt.Sprint(int(totpPeriod.Seconds()))},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPQRCode renders the otpauth URL as a PNG data: URI.
func TOTPQRCode(otpURL string) (string, error) {
	code, err := qr.Encode(otpURL, qr.M)
	if err != nil {
		return "", err
	}
	code.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()), nil
}

func totpStep(t time.Time) int64 { return t.Unix() / int64(totpPeriod.Seconds()) }

// totpCode computes the code for one time step (RFC 4226 HOTP).
func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// CheckTOTP reports whether code is valid for secret at time now, and if so
// the time step it matched. Callers record the step and reject codes for that
// step or earlier, so a code cannot be used twice.
func CheckTOTP(secret, code string, now time.Time) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	cur := totpStep(now)
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(key, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
