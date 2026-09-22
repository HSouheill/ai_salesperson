// Package auth provides password hashing and minimal HS256 JWTs.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Cost is the bcrypt work factor. Tests lower it; production keeps the default.
var Cost = bcrypt.DefaultCost

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), Cost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

type Claims struct {
	UserID string `json:"uid"`
	OrgID  string `json:"org"`
	// Agency is set when an agency is viewing a client organization; it is the
	// agency's own org ID, kept so the session can be traced and restricted.
	Agency string `json:"agency,omitempty"`
	Exp    int64  `json:"exp"`
}

var ErrInvalidToken = errors.New("invalid or expired token")

var enc = base64.RawURLEncoding

const header = `{"alg":"HS256","typ":"JWT"}`

func Sign(secret string, c Claims, ttl time.Duration) (string, error) {
	c.Exp = time.Now().Add(ttl).Unix()
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signing := enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(body)
	return signing + "." + enc.EncodeToString(mac(secret, signing)), nil
}

func Verify(secret, token string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, ErrInvalidToken
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac(secret, parts[0]+"."+parts[1])) {
		return c, ErrInvalidToken
	}
	// Only accept the header we issue, so "alg" cannot be swapped.
	if h, err := enc.DecodeString(parts[0]); err != nil || string(h) != header {
		return c, ErrInvalidToken
	}
	body, err := enc.DecodeString(parts[1])
	if err != nil || json.Unmarshal(body, &c) != nil || time.Now().Unix() > c.Exp {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

func mac(secret, data string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return h.Sum(nil)
}
