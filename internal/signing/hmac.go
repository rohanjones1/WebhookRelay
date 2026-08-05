// Package signing implements a Stripe-style HMAC-SHA256 signature scheme
// used both for signing outbound deliveries (so customer endpoints can
// verify events really came from Webhook Relay) and for verifying inbound
// requests (so arbitrary callers can't queue events under a publisher's
// name).
//
// Signature header format:
//
//	X-Webhook-Signature: t=<unix_timestamp>,v1=<hex_hmac_sha256>
//
// The signed message is "<timestamp>.<raw_payload_bytes>" — including the
// timestamp in the signed content (not just the header) is what prevents an
// attacker from replaying an old, validly-signed request indefinitely; the
// verifier also rejects timestamps outside a tolerance window.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrMalformedHeader  = errors.New("signing: malformed signature header")
	ErrTimestampTooOld  = errors.New("signing: timestamp outside tolerance window")
	ErrSignatureMismatch = errors.New("signing: signature does not match")
)

// DefaultTolerance is how far a signed request's timestamp may drift from
// "now" before it's rejected as a possible replay.
const DefaultTolerance = 5 * time.Minute

// Sign computes the signature header value for the given payload and secret,
// using the current time as the signed timestamp.
func Sign(secret string, payload []byte) string {
	return SignAt(secret, payload, time.Now())
}

// SignAt is like Sign but with an explicit timestamp — mainly here so tests
// can produce deterministic output.
func SignAt(secret string, payload []byte, ts time.Time) string {
	timestamp := ts.Unix()
	mac := computeMAC(secret, timestamp, payload)
	return fmt.Sprintf("t=%d,v1=%s", timestamp, mac)
}

// Verify checks a signature header against the payload and secret, using
// DefaultTolerance for replay protection.
func Verify(secret string, payload []byte, header string) error {
	return VerifyWithTolerance(secret, payload, header, DefaultTolerance)
}

// VerifyWithTolerance is Verify with an explicit tolerance window.
func VerifyWithTolerance(secret string, payload []byte, header string, tolerance time.Duration) error {
	timestamp, gotMAC, err := parseHeader(header)
	if err != nil {
		return err
	}

	age := time.Since(time.Unix(timestamp, 0))
	if age < 0 {
		age = -age
	}
	if age > tolerance {
		return ErrTimestampTooOld
	}

	expectedMAC := computeMAC(secret, timestamp, payload)
	if !hmac.Equal([]byte(expectedMAC), []byte(gotMAC)) {
		return ErrSignatureMismatch
	}
	return nil
}

func computeMAC(secret string, timestamp int64, payload []byte) string {
	signedMessage := fmt.Sprintf("%d.%s", timestamp, payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedMessage))
	return hex.EncodeToString(mac.Sum(nil))
}

// parseHeader extracts the timestamp and v1 signature from a header value
// like "t=1700000000,v1=abcdef...".
func parseHeader(header string) (int64, string, error) {
	var timestamp int64
	var sig string

	parts := strings.Split(header, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, "", ErrMalformedHeader
			}
			timestamp = ts
		case "v1":
			sig = kv[1]
		}
	}

	if timestamp == 0 || sig == "" {
		return 0, "", ErrMalformedHeader
	}
	return timestamp, sig, nil
}
