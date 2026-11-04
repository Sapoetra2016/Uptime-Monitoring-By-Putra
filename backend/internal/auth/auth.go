package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Token is a minimal HMAC-signed, base64-encoded claims payload (JWT-like but
// dependency-free) used for authenticating API/websocket clients.
type Claims struct {
	Sub      string `json:"sub"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

type Authenticator struct {
	secret []byte
	TTL    time.Duration
}

func New(secret string) *Authenticator {
	return &Authenticator{secret: []byte(secret), TTL: 24 * time.Hour}
}

func (a *Authenticator) Sign(claims Claims) (string, error) {
	claims.Exp = time.Now().Add(a.TTL).Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := a.sign(encoded)
	return encoded + "." + sig, nil
}

func (a *Authenticator) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("malformed token")
	}
	encoded, sig := parts[0], parts[1]
	if !a.verify(encoded, sig) {
		return nil, errors.New("invalid signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if time.Unix(claims.Exp, 0).Before(time.Now()) {
		return nil, errors.New("token expired")
	}
	return &claims, nil
}

func (a *Authenticator) sign(data string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) verify(data, sig string) bool {
	expected := a.sign(data)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// GeneratePushToken creates a random token for push/heartbeat monitors.
func GeneratePushToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
