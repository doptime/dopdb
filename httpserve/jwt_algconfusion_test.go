package httpserve

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"
)

// Algorithm confusion (the CVE-2016-10555 family).
//
// An RS256 deployment configures a PUBLIC key — published in JWKS, frequently
// shipped to the frontend. A verifier that reads `alg` out of the token will
// happily treat that public PEM as an HMAC secret, so anyone holding the public
// key can mint {"alg":"HS256"} tokens with whatever claims they want, including
// the uid that owner-scoping trusts. That is a complete authentication and
// row-isolation bypass built from public material, which is why the algorithm is
// pinned by the configured key instead.

func rsaTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	body := hdr + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// forgeHS256 mints a token signed with `secret` as an HMAC key — the attack when
// `secret` is a public PEM.
func forgeHS256(secret string, claims map[string]any) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	body := hdr + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestJWTRejectsAlgConfusion(t *testing.T) {
	key, pubPEM := rsaTestKey(t)
	claims := map[string]any{"uid": "victim", "exp": time.Now().Add(time.Hour).Unix()}

	// the attack: HS256 signed with the public key everyone can read
	forged := forgeHS256(pubPEM, claims)
	if _, err := VerifyJWT(forged, pubPEM); err == nil {
		t.Fatal("SECURITY: an HS256 token signed with the RS256 public key was accepted — full auth bypass")
	}

	// a genuine RS256 token still verifies
	good := signRS256(t, key, claims)
	got, err := VerifyJWT(good, pubPEM)
	if err != nil {
		t.Fatalf("a valid RS256 token was rejected: %v", err)
	}
	if got["uid"] != "victim" {
		t.Errorf("claims = %v", got)
	}
}

func TestJWTHS256DeploymentRejectsRS256Header(t *testing.T) {
	key, _ := rsaTestKey(t)
	claims := map[string]any{"uid": "u1", "exp": time.Now().Add(time.Hour).Unix()}

	// An HS256 deployment must not accept a token merely because it claims RS256:
	// the secret is not a public key, so there is nothing to verify against.
	if _, err := VerifyJWT(signRS256(t, key, claims), "plain-hmac-secret"); err == nil {
		t.Fatal("an RS256-header token was accepted by an HS256 deployment")
	}
	// and the normal path still works
	tok, err := SignHS256(claims, "plain-hmac-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyJWT(tok, "plain-hmac-secret"); err != nil {
		t.Fatalf("a valid HS256 token was rejected: %v", err)
	}
}

// exp used to fail open in two ways: exp<=0 was read as "no expiry", and an exp
// that would not parse was ignored entirely.
func TestJWTExpFailsClosed(t *testing.T) {
	secret := "s"
	cases := []struct {
		name  string
		exp   any
		valid bool
	}{
		{"future", time.Now().Add(time.Hour).Unix(), true},
		{"past", time.Now().Add(-time.Hour).Unix(), false},
		{"zero", 0, false},
		{"negative", -1, false},
		{"numeric string future", "9999999999", true},
		{"numeric string past", "1", false},
		{"unparseable", "not-a-number", false},
	}
	for _, c := range cases {
		tok, err := SignHS256(map[string]any{"uid": "u", "exp": c.exp}, secret)
		if err != nil {
			t.Fatal(err)
		}
		_, err = VerifyJWT(tok, secret)
		if (err == nil) != c.valid {
			t.Errorf("exp=%v (%s): accepted=%v want %v (err=%v)", c.exp, c.name, err == nil, c.valid, err)
		}
	}
	// no exp at all is still allowed
	tok, _ := SignHS256(map[string]any{"uid": "u"}, secret)
	if _, err := VerifyJWT(tok, secret); err != nil {
		t.Errorf("a token without exp was rejected: %v", err)
	}
}
