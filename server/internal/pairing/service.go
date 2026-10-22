package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
)

var (
	ErrDisabled      = errors.New("pairing is not configured")
	ErrInvalidCode   = errors.New("invalid pairing code")
	ErrUnauthorized  = errors.New("session is missing or expired")
	ErrRateLimited   = errors.New("too many pairing attempts")
	ErrCapacity      = errors.New("session capacity reached")
	ErrLoginRequired = errors.New("WeChat login is required")
)

const maxSessions = 128

type Session struct {
	Token     string         `json:"token"`
	ExpiresAt time.Time      `json:"expiresAt"`
	User      *accounts.User `json:"user,omitempty"`
}

type Record struct {
	ExpiresAt time.Time
	UserID    string
}

type Repository interface {
	LoadSessions([32]byte, time.Time) (map[[32]byte]Record, error)
	SaveSession([32]byte, [32]byte, Record) error
	DeleteSession([32]byte) error
}

type Service struct {
	mu          sync.Mutex
	codeHash    [32]byte
	enabled     bool
	ttl         time.Duration
	now         func() time.Time
	sessions    map[[32]byte]Record
	windowStart time.Time
	attempts    int
	repo        Repository
	users       *accounts.Service
}

func NewPersistent(code string, ttl time.Duration, repo Repository) (*Service, error) {
	s := New(code, ttl)
	sessions, err := repo.LoadSessions(s.codeHash, s.now())
	if err != nil {
		return nil, err
	}
	s.sessions = sessions
	s.repo = repo
	return s, nil
}

func New(code string, ttl time.Duration) *Service {
	return &Service{
		codeHash: sha256.Sum256([]byte(code)),
		enabled:  code != "",
		ttl:      ttl,
		now:      time.Now,
		sessions: make(map[[32]byte]Record),
	}
}

func GenerateCode() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Service) Pair(code string) (Session, error) {
	return s.Login(context.Background(), code, "")
}

func (s *Service) WithUsers(users *accounts.Service) *Service {
	s.users = users
	return s
}

func (s *Service) Users() *accounts.Service { return s.users }

func (s *Service) Login(ctx context.Context, code, loginCode string) (Session, error) {
	if err := s.checkCode(code); err != nil {
		return Session{}, err
	}
	var user *accounts.User
	if s.users != nil {
		if loginCode == "" {
			return Session{}, ErrLoginRequired
		}
		u, err := s.users.Login(ctx, loginCode)
		if err != nil {
			return Session{}, err
		}
		user = &u
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, record := range s.sessions {
		if !now.Before(record.ExpiresAt) {
			if s.repo != nil {
				if err := s.repo.DeleteSession(key); err != nil {
					return Session{}, err
				}
			}
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= maxSessions {
		return Session{}, ErrCapacity
	}
	token, err := GenerateCode()
	if err != nil {
		return Session{}, err
	}
	record := Record{ExpiresAt: now.Add(s.ttl).UTC()}
	if user != nil {
		record.UserID = user.ID
	}
	key := sha256.Sum256([]byte(token))
	if s.repo != nil {
		if err := s.repo.SaveSession(key, s.codeHash, record); err != nil {
			return Session{}, err
		}
	}
	s.sessions[key] = record
	return Session{Token: token, ExpiresAt: record.ExpiresAt, User: user}, nil
}

func (s *Service) checkCode(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return ErrDisabled
	}
	now := s.now()
	if s.windowStart.IsZero() || !now.Before(s.windowStart.Add(time.Minute)) {
		s.windowStart, s.attempts = now, 0
	}
	if s.attempts >= 10 {
		return ErrRateLimited
	}
	s.attempts++
	hash := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(hash[:], s.codeHash[:]) != 1 {
		return ErrInvalidCode
	}
	return nil
}

func (s *Service) Validate(token string) error {
	_, err := s.Owner(context.Background(), token)
	return err
}

func (s *Service) Owner(ctx context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(token))
	record, ok := s.sessions[key]
	if !ok || !s.now().Before(record.ExpiresAt) {
		if ok && s.repo != nil {
			_ = s.repo.DeleteSession(key)
		}
		delete(s.sessions, key)
		return "", ErrUnauthorized
	}
	if s.users != nil {
		if record.UserID == "" {
			return "", ErrUnauthorized
		}
		user, err := s.users.Get(ctx, record.UserID)
		return user.ID, err
	}
	// Keep the exact old owner encoding for deployments without WeChat login.
	if record.UserID != "" {
		return "", ErrUnauthorized
	}
	return fmt.Sprintf("%x", key), nil
}

func (s *Service) Revoke(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(token))
	if s.repo != nil {
		if err := s.repo.DeleteSession(key); err != nil {
			return err
		}
	}
	delete(s.sessions, key)
	return nil
}
