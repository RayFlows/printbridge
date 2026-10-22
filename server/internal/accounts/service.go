package accounts

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrLogin       = errors.New("WeChat login failed")
	ErrUnavailable = errors.New("WeChat login unavailable")
	ErrBanned      = errors.New("user is banned")
	ErrForbidden   = errors.New("administrator required")
	ErrNotFound    = errors.New("user not found")
	ErrName        = errors.New("invalid name")
	ErrPaused      = errors.New("new printing is paused")
)

type User struct {
	ID           string    `json:"id"`
	AppID        string    `json:"-"`
	Name         string    `json:"name"`
	NamePrompted bool      `json:"namePrompted"`
	Banned       bool      `json:"banned"`
	IsAdmin      bool      `json:"isAdmin"`
	CreatedAt    time.Time `json:"createdAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

type Settings struct {
	PrintingEnabled bool `json:"printingEnabled"`
}

type Repository interface {
	LoginUser(context.Context, string, string, time.Time) (User, error)
	GetUser(context.Context, string) (User, error)
	ListUsers(context.Context) ([]User, error)
	UpdateName(context.Context, string, string) (User, error)
	SetBanned(context.Context, string, bool) (User, error)
	AccountSettings(context.Context) (Settings, error)
	SaveAccountSettings(context.Context, Settings) error
}

type LoginVerifier interface {
	OpenID(context.Context, string) (string, error)
}

type Service struct {
	repo    Repository
	login   LoginVerifier
	appID   string
	adminID string
}

func New(repo Repository, login LoginVerifier, appID, adminID string) *Service {
	return &Service{repo: repo, login: login, appID: appID, adminID: adminID}
}

func (s *Service) Login(ctx context.Context, code string) (User, error) {
	if code == "" || len(code) > 512 {
		return User{}, ErrLogin
	}
	openID, err := s.login.OpenID(ctx, code)
	if err != nil {
		return User{}, err
	}
	if openID == "" {
		return User{}, ErrLogin
	}
	u, err := s.repo.LoginUser(ctx, s.appID, openID, time.Now().UTC())
	return s.check(u, err)
}

func (s *Service) check(u User, err error) (User, error) {
	if err != nil {
		return User{}, err
	}
	if u.AppID != s.appID {
		return User{}, ErrNotFound
	}
	u.IsAdmin = s.adminID != "" && u.ID == s.adminID
	if u.Banned {
		return User{}, ErrBanned
	}
	return u, nil
}

func (s *Service) Get(ctx context.Context, id string) (User, error) {
	u, err := s.repo.GetUser(ctx, id)
	return s.check(u, err)
}

func (s *Service) Profile(ctx context.Context, id, name string) (User, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return User{}, err
	}
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 40 ||
		strings.ContainsFunc(name, unicode.IsControl) {
		return User{}, ErrName
	}
	u, err := s.repo.UpdateName(ctx, id, name)
	return s.check(u, err)
}

func (s *Service) RequireAdmin(ctx context.Context, actor string) error {
	u, err := s.Get(ctx, actor)
	if err != nil {
		return err
	}
	if !u.IsAdmin {
		return ErrForbidden
	}
	return nil
}

func (s *Service) Users(ctx context.Context, actor string) ([]User, error) {
	if err := s.RequireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	all, err := s.repo.ListUsers(ctx)
	users := []User{}
	for _, user := range all {
		if user.AppID == s.appID {
			user.IsAdmin = user.ID == s.adminID
			users = append(users, user)
		}
	}
	return users, err
}

func (s *Service) Ban(ctx context.Context, actor, target string, banned bool) (User, error) {
	if err := s.RequireAdmin(ctx, actor); err != nil {
		return User{}, err
	}
	if target == s.adminID {
		return User{}, ErrForbidden
	}
	user, err := s.repo.GetUser(ctx, target)
	if err != nil {
		return User{}, err
	}
	if user.AppID != s.appID {
		return User{}, ErrNotFound
	}
	return s.repo.SetBanned(ctx, target, banned)
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return s.repo.AccountSettings(ctx)
}

func (s *Service) Configure(ctx context.Context, actor string, settings Settings) (Settings, error) {
	if err := s.RequireAdmin(ctx, actor); err != nil {
		return Settings{}, err
	}
	err := s.repo.SaveAccountSettings(ctx, settings)
	return settings, err
}

func (s *Service) AllowPrinting(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.PrintingEnabled {
		return ErrPaused
	}
	return nil
}
