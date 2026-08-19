// Package httpserve is the dopdb HTTP layer: it makes CRUD disappear by exposing
// a closed command vocabulary over KVRocks, with JWT @-context binding,
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
	errAlgMismatch   = errors.New("dopdb/jwt: token alg does not match the configured key")
	errExpired       = errors.New("dopdb/jwt: token expired")
	errMissingSecret = errors.New("dopdb/jwt: no secret configured")
)

// keyKind classifies the configured secret so the verifier can PIN the algorithm
// instead of taking it from the token.
type keyKind struct {
	rsa *rsa.PublicKey // non-nil => this deployment is RS256
}

var keyKindCache sync.Map // secret -> keyKind

// classifySecret decides, once per distinct secret, which algorithm this
// deployment accepts. A PEM-encoded public key means RS256; anything else is an
// HMAC key and means HS256.
func classifySecret(secret string) keyKind {
	if v, ok := keyKindCache.Load(secret); ok {
		return v.(keyKind)
	}
	k := keyKind{}
	if pub, err := parseRSAPublicKey(secret); err == nil {
		k.rsa = pub
	}
	keyKindCache.Store(secret, k)
	return k
}

// VerifyJWT validates a compact JWS and returns its claims. HS256 uses secret as
// the raw HMAC key; RS256 expects secret to be a PEM-encoded PKIX public key.
// The "none" algorithm is rejected. exp (if present) is enforced.
//
// THE ALGORITHM IS PINNED BY THE CONFIGURED SECRET, not by the token header.
// Reading `alg` from the token is the classic confusion attack: an RS256
// deployment's key is a PUBLIC key — published in JWKS, often shipped to the
// frontend — so an attacker who takes that PEM text and uses it as an HMAC
// secret can mint `{"alg":"HS256"}` tokens with any claims they like, including
// the `uid` that owner-scoping trusts. A header-driven verifier accepts them.
// That is a complete authentication and row-isolation bypass from public
// material, so the header only gets to AGREE with the configured key kind.
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

	switch strings.ToLower(hdr.Alg) {
	case "none", "":
		return nil, errNoneAlg
	}

	kind := classifySecret(secret)
	if kind.rsa != nil {
		// RS256 deployment: an HS256 token is the confusion attack, not a
		// downgrade to support.
		if hdr.Alg != "RS256" {
			return nil, errAlgMismatch
		}
		sum := sha256.Sum256(signingInput)
		if err := rsa.VerifyPKCS1v15(kind.rsa, crypto.SHA256, sum[:], sig); err != nil {
			return nil, errBadSignature
		}
	} else {
		if hdr.Alg != "HS256" {
			return nil, errAlgMismatch
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(signingInput)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return nil, errBadSignature
		}
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
	var (
		exp int64
		err error
	)
	switch n := v.(type) {
	case json.Number:
		exp, err = n.Int64()
	case float64:
		exp = int64(n)
	case string:
		exp, err = strconv.ParseInt(n, 10, 64)
	default:
		err = errBadToken
	}
	// An exp that is present but unreadable is a malformed token, not an
	// unlimited one; and exp<=0 is in the past, not "no expiry". Both used to
	// fail open.
	if err != nil {
		return errBadToken
	}
	if exp <= time.Now().Unix() {
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
