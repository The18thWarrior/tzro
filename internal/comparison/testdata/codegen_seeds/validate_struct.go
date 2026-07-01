package codegen_seeds

import (
	"errors"
	"time"
)

type User struct {
	ID        string
	Email     string
	Name      string
	Age       int
	CreatedAt time.Time
}

// NewUser creates a User with the given fields and sets CreatedAt to now.
func NewUser(id, email, name string, age int) *User {
	return &User{
		ID:        id,
		Email:     email,
		Name:      name,
		Age:       age,
		CreatedAt: time.Now(),
	}
}

// DisplayName returns the user's display name, falling back to email.
func (u *User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func (u *User) Validate() error {
	if u.ID == "" {
		return errors.New("ID cannot be empty")
	}
	if u.Email == "" {
		return errors.New("email cannot be empty")
	}
	if u.Name == "" {
		return errors.New("name cannot be empty")
	}
	if u.Age <= 0 {
		return errors.New("age must be greater than zero")
	}
	if u.CreatedAt.IsZero() {
		return errors.New("created at cannot be zero")
	}
	return nil
}
