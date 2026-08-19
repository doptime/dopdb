import { test } from "node:test";
import assert from "node:assert/strict";
import { createHmac, createSign, generateKeyPairSync } from "node:crypto";

import { _verifyJWTForTests as verifyJWT } from "../src/server.js";

// Algorithm confusion (the CVE-2016-10555 family).
//
// An RS256 deployment configures a PUBLIC key — published in JWKS, frequently
// shipped to the frontend. A verifier that reads `alg` out of the token will
// treat that public PEM as an HMAC secret, so anyone holding the public key can
// mint {"alg":"HS256"} tokens with whatever claims they want, including the uid
// owner-scoping trusts. Complete auth bypass from public material.

const b64 = (b: Buffer | string) => Buffer.from(b).toString("base64url");

function forgeHS256(secret: string, claims: Record<string, unknown>): string {
  const body = `${b64('{"alg":"HS256","typ":"JWT"}')}.${b64(JSON.stringify(claims))}`;
  return `${body}.${createHmac("sha256", secret).update(body).digest("base64url")}`;
}

function signRS256(privateKey: string, claims: Record<string, unknown>): string {
  const body = `${b64('{"alg":"RS256","typ":"JWT"}')}.${b64(JSON.stringify(claims))}`;
  const sig = createSign("RSA-SHA256").update(body).sign(privateKey);
  return `${body}.${sig.toString("base64url")}`;
}

const rsaPair = () => {
  const { publicKey, privateKey } = generateKeyPairSync("rsa", {
    modulusLength: 2048,
    publicKeyEncoding: { type: "spki", format: "pem" },
    privateKeyEncoding: { type: "pkcs8", format: "pem" },
  });
  return { publicKey, privateKey };
};

const future = () => Math.floor(Date.now() / 1000) + 3600;

test("an HS256 token signed with the RS256 public key is rejected", () => {
  const { publicKey, privateKey } = rsaPair();
  const claims = { uid: "victim", exp: future() };

  assert.throws(
    () => verifyJWT(forgeHS256(publicKey, claims), publicKey),
    /alg does not match|unsupported|bad signature/,
    "SECURITY: alg-confusion token accepted — full auth bypass",
  );
  // a genuine RS256 token still verifies
  const ok = verifyJWT(signRS256(privateKey, claims), publicKey);
  assert.equal(ok.uid, "victim");
});

test("an HS256 deployment rejects an RS256-header token", () => {
  const { privateKey } = rsaPair();
  const claims = { uid: "u1", exp: future() };
  assert.throws(() => verifyJWT(signRS256(privateKey, claims), "plain-hmac-secret"), /alg does not match/);
  // and the normal path still works
  assert.equal(verifyJWT(forgeHS256("plain-hmac-secret", claims), "plain-hmac-secret").uid, "u1");
});

// A malformed token is a client error. It used to escape JSON.parse and surface
// as a 500, which both hides the cause and diverges from the Go engine.
test("malformed tokens are unauthorized, never a server error", () => {
  for (const bad of ["garbage", "a.b", "a.b.c", "%%%.%%%.%%%", "...", `${b64("not json")}.${b64("{}")}.x`]) {
    assert.throws(() => verifyJWT(bad, "s"), (e: Error) => {
      assert.equal((e as { status?: number }).status, 401, `"${bad}" should be 401, got ${(e as { status?: number }).status}`);
      return true;
    });
  }
});

// exp used to fail open: exp<=0 read as "no expiry" on Go, string exp ignored on TS.
test("exp fails closed and accepts the same shapes as Go", () => {
  const cases: [unknown, boolean][] = [
    [future(), true],
    [Math.floor(Date.now() / 1000) - 3600, false],
    [0, false],
    [-1, false],
    ["9999999999", true],
    ["1", false],
    ["not-a-number", false],
  ];
  for (const [exp, valid] of cases) {
    const tok = forgeHS256("s", { uid: "u", exp });
    let accepted = true;
    try { verifyJWT(tok, "s"); } catch { accepted = false; }
    assert.equal(accepted, valid, `exp=${JSON.stringify(exp)} accepted=${accepted} want ${valid}`);
  }
  // no exp at all is still allowed
  assert.equal(verifyJWT(forgeHS256("s", { uid: "u" }), "s").uid, "u");
});
