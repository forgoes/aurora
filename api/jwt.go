package api

import (
	"github.com/golang-jwt/jwt/v5"
)

type JWTClaims struct {
	UserId    uint     `json:"id,omitempty"`
	UserName  string   `json:"name,omitempty"`
	UserEmail string   `json:"email,omitempty"`
	UserRole  string   `json:"user_role,omitempty"`
	MetaMasks []string `json:"meta_masks,omitempty"`

	jwt.Claims
}
