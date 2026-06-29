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
