// Package profile exposes the single local user's display identity. It is
// stored in the generic settings table; no separate profile table is needed for
// a single-user local application.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/theDenuvo/LifeOS/internal/application/settings"
)

const (
	// NameKey is the settings key for the display name.
	NameKey = "profile.name"
	// RoleKey is the settings key for the display role.
	RoleKey = "profile.role"

	// DefaultName is shown until the user sets a name.
	DefaultName = "Пользователь"
	// DefaultRole is shown until the user sets a role.
	DefaultRole = "Личный профиль"

	maxNameLength = 80
	maxRoleLength = 80
)

// Errors.
var (
	ErrInvalidName = errors.New("name is required and must be 1-80 characters")
	ErrInvalidRole = errors.New("role must be at most 80 characters")
)

// Profile is the public display identity.
type Profile struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Initials string `json:"initials"`
}

// Store is the settings slice the profile service needs.
type Store interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
	Put(ctx context.Context, key string, value json.RawMessage) error
}

// Service reads and writes the profile.
type Service struct {
	store Store
}

// NewService builds a profile service.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Get returns the current profile, falling back to neutral defaults.
func (s *Service) Get(ctx context.Context) (Profile, error) {
	name, err := s.read(ctx, NameKey, DefaultName)
	if err != nil {
		return Profile{}, err
	}
	role, err := s.read(ctx, RoleKey, DefaultRole)
	if err != nil {
		return Profile{}, err
	}
	return newProfile(name, role), nil
}

// Update validates and stores a new profile.
func (s *Service) Update(ctx context.Context, name, role string) (Profile, error) {
	name = strings.TrimSpace(name)
	role = strings.TrimSpace(role)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) == 0 || utf8.RuneCountInString(name) > maxNameLength {
		return Profile{}, ErrInvalidName
	}
	if !utf8.ValidString(role) || utf8.RuneCountInString(role) > maxRoleLength {
		return Profile{}, ErrInvalidRole
	}
	if role == "" {
		role = DefaultRole
	}

	if err := s.write(ctx, NameKey, name); err != nil {
		return Profile{}, err
	}
	if err := s.write(ctx, RoleKey, role); err != nil {
		return Profile{}, err
	}
	return newProfile(name, role), nil
}

func (s *Service) read(ctx context.Context, key, fallback string) (string, error) {
	raw, err := s.store.Get(ctx, key)
	if errors.Is(err, settings.ErrNotFound) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback, nil
	}
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return value, nil
}

func (s *Service) write(ctx context.Context, key, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, key, encoded)
}

func newProfile(name, role string) Profile {
	return Profile{Name: name, Role: role, Initials: initials(name)}
}

func initials(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "?"
	}
	first := firstRuneUpper(fields[0])
	if len(fields) == 1 {
		return first
	}
	return first + firstRuneUpper(fields[len(fields)-1])
}

func firstRuneUpper(value string) string {
	for _, r := range value {
		return strings.ToUpper(string(r))
	}
	return "?"
}
