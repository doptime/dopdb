// Package httpserve is the dopdb HTTP layer: it makes CRUD disappear by exposing
// a closed command vocabulary over MongoDB, with JWT @-context binding,
// per-(command,collection) permissions, and row-level owner scoping.
package httpserve

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Claims is a decoded, verified JWT claim set. Numeric values are json.Number to
// avoid the float64 precision / scientific-notation hazard the doptime docs warn
// about with numeric ids.
type Claims map[string]any

var (
	errBadToken      = errors.New("dopdb/jwt: malformed token")
	errBadSignature  = errors.New("dopdb/jwt: signature invalid")
	errNoneAlg       = errors.New("dopdb/jwt: alg \"none\" is not allowed")
	errUnsupported   = errors.New("dopdb/jwt: unsupported alg")
	errExpired       = errors.New("dopdb/jwt: token expired")
	errMissingSecret = errors.New("dopdb/jwt: no secret configured")
)

// VerifyJWT validates a compact JWS and returns its claims. HS256 uses secret as
// the raw HMAC key; RS256 expects secret to be a PEM-encoded PKIX public key.
// The "none" algorithm is rejected. exp (if present) is enforced.
func VerifyJWT(token, secret string) (Claims, error) {
	if secret == "" {
		return nil, errMissingSecret
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errBadToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errBadToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, errBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errBadToken
	}
	signingInput := []byte(parts[0] + "." + parts[1])

	switch hdr.Alg {
	case "HS256":
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(signingInput)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return nil, errBadSignature
		}
	case "RS256":
		pub, err := parseRSAPublicKey(secret)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(signingInput)
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
			return nil, errBadSignature
		}
	case "none", "None", "NONE":
		return nil, errNoneAlg
	default:
		return nil, errUnsupported
	}

	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errBadToken
	}
	claims := Claims{}
	dec := json.NewDecoder(bytes.NewReader(claimBytes))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return nil, errBadToken
	}
	if err := checkExp(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("dopdb/jwt: failed to parse PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("dopdb/jwt: parse public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("dopdb/jwt: not an RSA public key")
	}
	return rsaPub, nil
}

func checkExp(claims Claims) error {
	v, ok := claims["exp"]
	if !ok {
		return nil
	}
	var exp int64
	switch n := v.(type) {
	case json.Number:
		exp, _ = n.Int64()
	case float64:
		exp = int64(n)
	case string:
		exp, _ = strconv.ParseInt(n, 10, 64)
	}
	if exp > 0 && exp < time.Now().Unix() {
		return errExpired
	}
	return nil
}

// SignHS256 mints an HS256 token from claims. Provided for your login endpoint
// (the analogue of doptime's ConvertMapToJwtString); also used by tests.
func SignHS256(claims map[string]any, secret string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ---- small verified-token cache (swap for hashicorp/golang-lru if desired) ----

type tokenCache struct {
	mu  sync.RWMutex
	m   map[string]Claims
	cap int
}

func newTokenCache(capacity int) *tokenCache {
	return &tokenCache{m: make(map[string]Claims), cap: capacity}
}

func (t *tokenCache) get(tok string) (Claims, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.m[tok]
	return c, ok
}

func (t *tokenCache) put(tok string, c Claims) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.m) >= t.cap { // crude eviction: drop everything when full
		t.m = make(map[string]Claims)
	}
	t.m[tok] = c
}
