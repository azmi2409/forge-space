// Package auth: password hashing (PBKDF2-SHA256 310k), opaque session tokens,
// agent API keys (fspk_), and caller resolution middleware helpers.
// Secrets are never stored raw — only sha256 hashes reach the database.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/azmi2409/forge-space/internal/store"
)

const (
	// PBKDF2 parameters per CONTRACT.md §10.
	pbkdf2Iterations = 310_000
	saltLen          = 16
	keyLen           = 32
	// AgentKeyPrefix is the public prefix of agent API keys.
	AgentKeyPrefix = "fspk_"
	// sessionTokenBytes is the entropy of opaque session tokens (hex-encoded).
	sessionTokenBytes = 32
	// SessionTTL is 30 days per CONTRACT.md §1.
	SessionTTL = 30 * 24 * time.Hour
)

// Scopes
const (
	ScopeAgent = "agent"
	ScopeAdmin = "admin"
)

// HashPassword hashes a password with PBKDF2-SHA256 310k.
// Format: pbkdf2-sha256$310000$<base64 salt>$<base64 key>
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("auth: password must be at least 8 characters")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: rand: %w", err)
	}
	key := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, keyLen, sha256.New)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a stored hash. Wrong password
// returns false, nil (no error) — callers map that to 401.
func VerifyPassword(password, hash string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false, errors.New("auth: unrecognized password hash format")
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil {
		return false, errors.New("auth: bad iteration count")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errors.New("auth: bad salt encoding")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, errors.New("auth: bad key encoding")
	}
	got := pbkdf2.Key([]byte(password), salt, iter, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NewSessionToken generates an opaque 32-byte hex token (returned to the
// client) and its sha256 hex digest (stored in the DB).
func NewSessionToken() (token, tokenHash string, err error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: rand: %w", err)
	}
	token = hex.EncodeToString(b)
	return token, SHA256Hex(token), nil
}

// NewAgentKey generates an agent API key: "fspk_" + 32 base36 chars.
// Returns the full secret (shown ONCE) and its sha256 hex digest for storage.
func NewAgentKey() (secret, secretHash string, err error) {
	// Reuse the store's base36 ID generator for the random suffix.
	suffix := store.NewID("")
	if len(suffix) != 16 {
		return "", "", errors.New("auth: bad random suffix length")
	}
	// 16 chars is fine, but give agent secrets 32 chars of entropy.
	suffix2 := store.NewID("")
	if len(suffix2) != 16 {
		return "", "", errors.New("auth: bad random suffix length")
	}
	secret = AgentKeyPrefix + suffix + suffix2
	return secret, SHA256Hex(secret), nil
}

// SHA256Hex returns the hex sha256 of s.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Caller describes who is making a request after bearer resolution.
type Caller struct {
	UserID     string   // set for human session tokens
	AgentKeyID string   // set for agent keys
	KeyName    string   // agent key name (for logs/display)
	Scopes     []string // agent key scopes; nil for human tokens
}

// IsAgent reports whether the caller authenticated with an agent key.
func (c Caller) IsAgent() bool { return c.AgentKeyID != "" }

// HasScope reports whether the caller holds a scope (humans hold none;
// use explicit owner checks for human admin rights).
func (c Caller) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// StoreLookups is the minimal store surface auth needs (avoids import cycles).
type StoreLookups interface {
	GetSession(tokenHash string) (*store.Session, error)
	GetAgentKeyBySecretHash(hash string) (*store.AgentKeyFull, error)
}

// Resolve maps an Authorization bearer value to a Caller.
// bearer is the raw token (without the "Bearer " prefix).
// Returns ErrUnauthorized when the credential is missing/invalid/expired.
var ErrUnauthorized = errors.New("auth: unauthorized")

// Resolve maps a bearer token to its caller.
func Resolve(st StoreLookups, bearer string) (Caller, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return Caller{}, ErrUnauthorized
	}
	if strings.HasPrefix(bearer, AgentKeyPrefix) {
		k, err := st.GetAgentKeyBySecretHash(SHA256Hex(bearer))
		if err != nil {
			return Caller{}, ErrUnauthorized
		}
		return Caller{
			UserID:     k.UserID,
			AgentKeyID: k.ID,
			KeyName:    k.Name,
			Scopes:     append([]string(nil), k.Scopes...),
		}, nil
	}
	se, err := st.GetSession(SHA256Hex(bearer))
	if err != nil {
		return Caller{}, ErrUnauthorized
	}
	if exp, err := time.Parse(time.RFC3339, se.ExpiresAt); err != nil || time.Now().UTC().After(exp) {
		return Caller{}, ErrUnauthorized
	}
	return Caller{UserID: se.UserID}, nil
}

// BearerToken extracts the bearer value from an Authorization header.
func BearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
