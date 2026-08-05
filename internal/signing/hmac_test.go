package signing

import (
	"strings"
	"testing"
	"time"
)

func TestSignThenVerify_Succeeds(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"hello":"world"}`)

	header := Sign(secret, payload)

	if err := Verify(secret, payload, header); err != nil {
		t.Fatalf("expected verification to succeed, got error: %v", err)
	}
}

func TestVerify_WrongSecret_Fails(t *testing.T) {
	payload := []byte(`{"hello":"world"}`)
	header := Sign("correct-secret", payload)

	err := Verify("wrong-secret", payload, header)
	if err != ErrSignatureMismatch {
		t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
	}
}

func TestVerify_TamperedPayload_Fails(t *testing.T) {
	secret := "test-secret"
	original := []byte(`{"amount":10}`)
	tampered := []byte(`{"amount":10000}`)

	header := Sign(secret, original)

	err := Verify(secret, tampered, header)
	if err != ErrSignatureMismatch {
		t.Fatalf("expected ErrSignatureMismatch for tampered payload, got: %v", err)
	}
}

func TestVerify_MalformedHeader_Fails(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"hello":"world"}`)

	cases := []string{
		"",
		"garbage",
		"t=notanumber,v1=abc123",
		"v1=abc123", // missing timestamp
		"t=1700000000",  // missing signature
	}

	for _, header := range cases {
		if err := Verify(secret, payload, header); err == nil {
			t.Errorf("expected error for malformed header %q, got nil", header)
		}
	}
}

func TestVerify_StaleTimestamp_Fails(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"hello":"world"}`)

	// Signed 10 minutes ago — outside the default 5-minute tolerance.
	staleTime := time.Now().Add(-10 * time.Minute)
	header := SignAt(secret, payload, staleTime)

	err := Verify(secret, payload, header)
	if err != ErrTimestampTooOld {
		t.Fatalf("expected ErrTimestampTooOld, got: %v", err)
	}
}

func TestVerify_FutureTimestamp_Fails(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"hello":"world"}`)

	// Clock skew / forged future timestamp — should also be rejected since
	// tolerance is checked as an absolute difference from "now".
	futureTime := time.Now().Add(10 * time.Minute)
	header := SignAt(secret, payload, futureTime)

	err := Verify(secret, payload, header)
	if err != ErrTimestampTooOld {
		t.Fatalf("expected ErrTimestampTooOld for future timestamp, got: %v", err)
	}
}

func TestVerify_WithinToleranceWindow_Succeeds(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"hello":"world"}`)

	// 2 minutes old — inside the default 5-minute tolerance.
	recentTime := time.Now().Add(-2 * time.Minute)
	header := SignAt(secret, payload, recentTime)

	if err := Verify(secret, payload, header); err != nil {
		t.Fatalf("expected verification to succeed within tolerance, got: %v", err)
	}
}

func TestSign_HeaderFormat(t *testing.T) {
	header := Sign("secret", []byte("payload"))

	if !strings.HasPrefix(header, "t=") {
		t.Errorf("expected header to start with 't=', got: %s", header)
	}
	if !strings.Contains(header, ",v1=") {
		t.Errorf("expected header to contain ',v1=', got: %s", header)
	}
}
