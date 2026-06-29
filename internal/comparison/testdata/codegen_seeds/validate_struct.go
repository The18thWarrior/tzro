package codegen_seeds

import (
	"errors"
	"strings"
	"time"
)

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Age       int       `json:"age"`
	CreatedAt time.Time `json:"createdAt"`
}

func NewUser(id, email, name string, age int) *User {
	return &User{
		ID:        id,
		Email:     email,
		Name:      name,
		Age:       age,
		CreatedAt: time.Now(),
	}
}

func (u *User) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func (u *User) Validate() error {
	if u.Email == "" {
		return errors.New("email cannot be empty")
	}
	if !strings.Contains(u.Email, "@") {
		return errors.New("email must contain @")
	}
	if u.Age < 0 {
		return errors.New("age must be greater than or equal to 0")
	}
	return nil
}
