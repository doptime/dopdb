#!/usr/bin/env node
// dopdb-spec — emit the schema as data (dopdb.schema.json).
//
//   node --import tsx bin/spec.ts ./src/shared/schema.ts > dopdb.schema.json
//   # or, after build:  node dist/bin/spec.js ./schema.js
//
// The module must export `schema` (or a default export) — a map of collections.
// The emitted JSON is the single artifact a non-TS engine (e.g. the optional Go
// gateway) reads. It is derived from the one TS source; it is not a second
// definition.

import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { buildSpec, type Collection } from "../src/schema.js";

async function main(): Promise<void> {
  const arg = process.argv[2];
  if (!arg) {
    process.stderr.write("usage: dopdb-spec <path-to-schema-module>\n");
    process.exit(2);
  }
  const mod = (await import(pathToFileURL(resolve(arg)).href)) as Record<string, unknown>;
  const schema = (mod.schema ?? mod.default) as Record<string, Collection<any>> | undefined;
  if (!schema || typeof schema !== "object") {
    process.stderr.write(`module ${arg} must export \`schema\` (or default) — a map of collections\n`);
    process.exit(1);
  }
  process.stdout.write(JSON.stringify(buildSpec(schema), null, 2) + "\n");
}

void main();
