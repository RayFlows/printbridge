package pairing

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const testCode = "test-only-pairing-code-0123456789012345"

func TestPairValidateRevokeAndExpire(t *testing.T) {
	s := New(testCode, time.Hour)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.Pair("wrong"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: %v", err)
	}
	first, err := s.Pair(testCode)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Pair(testCode)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token || first.Token == testCode || len(first.Token) != 43 {
		t.Fatal("sessions must use unique, independently generated tokens")
	}
	if !first.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expiry = %s", first.ExpiresAt)
	}
	for _, token := range []string{"", testCode, "unknown"} {
		if !errors.Is(s.Validate(token), ErrUnauthorized) {
			t.Fatal("non-session credential accepted")
		}
	}
	if err := s.Validate(first.Token); err != nil {
		t.Fatal(err)
	}
	s.Revoke(first.Token)
	s.Revoke(first.Token)
	if !errors.Is(s.Validate(first.Token), ErrUnauthorized) {
		t.Fatal("revoked session is still valid")
	}
	now = second.ExpiresAt
	if !errors.Is(s.Validate(second.Token), ErrUnauthorized) {
		t.Fatal("session must expire at its exact expiry time")
	}
}

func TestDisabledRestartAndRateLimit(t *testing.T) {
	if _, err := New("", time.Hour).Pair(testCode); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled pairing: %v", err)
	}
	s := New(testCode, time.Hour)
	now := time.Now()
	s.now = func() time.Time { return now }
	token, err := s.Pair(testCode)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(New(testCode, time.Hour).Validate(token.Token), ErrUnauthorized) {
		t.Fatal("a new instance accepted an old session")
	}
	for i := 0; i < 9; i++ {
		if _, err := s.Pair("wrong"); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.Pair(testCode); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := s.Pair(testCode); err != nil {
		t.Fatalf("rate limit did not reset: %v", err)
	}
}

func TestCapacityAndExpiredSessionCleanup(t *testing.T) {
	s := New(testCode, 24*time.Hour)
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < maxSessions; i++ {
		if _, err := s.Pair(testCode); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if _, err := s.Pair(testCode); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := s.Pair(testCode); err != nil || len(s.sessions) != 1 {
		t.Fatalf("expired sessions not reclaimed: count=%d, error=%v", len(s.sessions), err)
	}
}

func TestConcurrentValidationAndRevocation(t *testing.T) {
	s := New(testCode, time.Hour)
	session, err := s.Pair(testCode)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Go(func() {
			for j := 0; j < 10; j++ {
				_ = s.Validate(session.Token)
				s.Revoke(session.Token)
			}
		})
	}
	workers.Wait()
	if !errors.Is(s.Validate(session.Token), ErrUnauthorized) {
		t.Fatal("revocation was lost")
	}
}

func TestGenerateCode(t *testing.T) {
	code, err := GenerateCode()
	if err != nil || len(code) != 43 || strings.ContainsAny(code, "/+= \n") {
		t.Fatalf("unexpected generated code format: error=%v", err)
	}
}
