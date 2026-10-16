package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/azmi2409/forge-space/internal/store"
)

func TestPasswordRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$310000$") {
		t.Fatalf("unexpected hash format: %s", hash[:30])
	}
	ok, err := VerifyPassword("correct-horse-8", hash)
	if err != nil || !ok {
		t.Fatalf("valid password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong-password", hash)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("wrong password accepted")
	}
	// salts differ → hashes differ
	hash2, _ := HashPassword("correct-horse-8")
	if hash == hash2 {
		t.Fatal("two hashes of the same password must differ (salt)")
	}
}

func TestPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
}

func TestSessionTokenFormat(t *testing.T) {
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token len = %d, want 64 hex chars", len(token))
	}
	if tokenHash != SHA256Hex(token) {
		t.Fatal("stored hash must be sha256 of the token")
	}
}

func TestAgentKeyFormat(t *testing.T) {
	secret, secretHash, err := NewAgentKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "fspk_") {
		t.Fatalf("secret must start with fspk_, got %s", secret[:10])
	}
	if len(secret) != len("fspk_")+32 {
		t.Fatalf("secret len = %d, want 37", len(secret))
	}
	if secretHash != SHA256Hex(secret) {
		t.Fatal("stored hash must be sha256 of the full secret")
	}
}

// fakeStore implements StoreLookups for Resolve tests.
type fakeStore struct {
	sessions map[string]*store.Session
	keys     map[string]*store.AgentKeyFull
}

func (f *fakeStore) GetSession(h string) (*store.Session, error) {
	if s, ok := f.sessions[h]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GetAgentKeyBySecretHash(h string) (*store.AgentKeyFull, error) {
	if k, ok := f.keys[h]; ok {
		return k, nil
	}
	return nil, store.ErrNotFound
}

func TestResolve(t *testing.T) {
	f := &fakeStore{sessions: map[string]*store.Session{}, keys: map[string]*store.AgentKeyFull{}}

	// human session
	tok, tokHash, _ := NewSessionToken()
	f.sessions[tokHash] = &store.Session{
		TokenHash: tokHash, UserID: "us_abc",
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	c, err := Resolve(f, tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != "us_abc" || c.IsAgent() {
		t.Fatalf("bad caller: %+v", c)
	}

	// expired session
	tok2, tokHash2, _ := NewSessionToken()
	f.sessions[tokHash2] = &store.Session{
		TokenHash: tokHash2, UserID: "us_abc",
		ExpiresAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	}
	if _, err := Resolve(f, tok2); err != ErrUnauthorized {
		t.Fatal("expired session must be unauthorized")
	}

	// agent key with scopes
	secret, secretHash, _ := NewAgentKey()
	f.keys[secretHash] = &store.AgentKeyFull{
		AgentKey: store.AgentKey{ID: "ak_1", UserID: "us_abc", Name: "bot", Scopes: []string{"agent"}},
	}
	c, err = Resolve(f, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsAgent() || c.AgentKeyID != "ak_1" || !c.HasScope("agent") || c.HasScope("admin") {
		t.Fatalf("bad agent caller: %+v", c)
	}

	// unknown credentials
	for _, bad := range []string{"", "nope", "fspk_nonexistentsecret00000000000000"} {
		if _, err := Resolve(f, bad); err != ErrUnauthorized {
			t.Fatalf("credential %q must be unauthorized", bad)
		}
	}
}

func TestBearerToken(t *testing.T) {
	if got := BearerToken("Bearer abc123"); got != "abc123" {
		t.Fatalf("got %q", got)
	}
	if got := BearerToken("bearer abc123"); got != "abc123" {
		t.Fatalf("scheme should be case-insensitive, got %q", got)
	}
	if got := BearerToken("Token abc"); got != "" {
		t.Fatalf("wrong scheme must yield empty, got %q", got)
	}
	if got := BearerToken(""); got != "" {
		t.Fatalf("empty header must yield empty, got %q", got)
	}
}
