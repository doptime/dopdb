import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import { resolve, dirname } from "node:path";

const srcDir = resolve(dirname(new URL(import.meta.url).pathname), "../src");

// ---------------------------------------------------------------------------
// Helpers: parse a .ts file for local ".js" imports and for forbidden imports
// ---------------------------------------------------------------------------

/** Regex matching: import ... from "./foo.js" or import ... from '../bar.js' */
const LOCAL_IMPORT_RE = /from\s+(?<q>["'])(?<path>\.\/[^"']+|(\.\.\/)+[^"']+)\k<q>/g;

/** Regex matching: import ... from "mongodb" or require("mongodb") */
const MONGODB_IMPORT_RE = /(?:from\s+["']mongodb["']|require\s*\(\s*["']mongodb["'])/;

/** Regex matching: import ... from "node:*" (node:fs, node:crypto, node:http, …) */
const NODE_BUILTIN_IMPORT_RE = /from\s+["']node:[^"']+["']/;

/**
 * Strip single-line (//) and block comments so that import
 * patterns inside comments are not picked up by the regex scanners.
 */
function stripComments(source: string): string {
	const lines = source.split("\n");
	let inBlock = false;
	return lines.map((line) => {
		if (inBlock) {
			const end = line.indexOf("*/");
			if (end !== -1) {
				inBlock = false;
				return line.slice(end + 2);
			}
			return "";
		}
		if (line.includes("/*")) {
			const start = line.indexOf("/*");
			const rest = line.slice(start + 2);
			const end = rest.indexOf("*/");
			if (end !== -1) {
				return line.slice(0, start) + rest.slice(end + 2);
			}
			inBlock = true;
			return line.slice(0, start);
		}
		const sl = line.trimStart();
		if (sl.startsWith("//")) {
			return "";
		}
		// remove trailing single-line comment (naive: after a quote-end)
		const commentPos = findLineComment(line);
		if (commentPos !== -1) {
			return line.slice(0, commentPos);
		}
		return line;
	}).join("\n");
}

/** Find position of a trailing // comment, skipping quoted strings. */
function findLineComment(line: string): number {
	let inSingle = false, inDouble = false;
	for (let i = 0; i < line.length - 1; i++) {
		const c = line[i];
		if (c === "'" && !inDouble) inSingle = !inSingle;
		else if (c === '"' && !inSingle) inDouble = !inDouble;
		else if (c === "/" && line[i + 1] === "/" && !inSingle && !inDouble) return i;
	}
	return -1;
}

/** Collect all local .js imports from a single source file (ignoring comments). */
function getLocalImports(filePath: string): string[] {
	const content = stripComments(readFileSync(filePath, "utf-8"));
	const results: string[] = [];
	let m;
	const re = new RegExp(LOCAL_IMPORT_RE.source, LOCAL_IMPORT_RE.flags);
	while ((m = re.exec(content)) !== null) {
		results.push(m.groups!.path);
	}
	return results;
}

/** Check if a source file imports mongodb or any node:* builtin. */
function hasServerOnlyImport(filePath: string): { hasMongoDB: boolean; hasNodeBuiltin: boolean; nodeModules: string[] } {
	const content = stripComments(readFileSync(filePath, "utf-8"));
	const hasMongoDB = MONGODB_IMPORT_RE.test(content);
	const hasNodeBuiltin = NODE_BUILTIN_IMPORT_RE.test(content);
	const nodeModules: string[] = [];
	let m;
	const re = /from\s+["'](node:[^"']+)["']/g;
	while ((m = re.exec(content)) !== null) {
		nodeModules.push(m[1]);
	}
	return { hasMongoDB, hasNodeBuiltin, nodeModules };
}

/**
 * Recursively resolve the full transitive import graph starting from an entry
 * file, following only local `.js` imports. ESM `.ts` sources write `.js` in
 * import specifiers, so the resolver rewrites `.js` → `.ts` on disk.
 */
function buildImportGraph(entryFile: string): Set<string> {
	const visited = new Set<string>();
	const stack = [entryFile];

	while (stack.length) {
		const current = stack.pop()!;
		if (visited.has(current)) continue;
		visited.add(current);

		if (!existsSync(current)) continue;
		for (const rel of getLocalImports(current)) {
			// ESM .ts sources use .js in specifiers; map back to .ts on disk.
			const tsRel = rel.replace(/\.js$/, ".ts");
			const abs = resolve(dirname(current), tsRel);
			if (!visited.has(abs)) {
				stack.push(abs);
			}
		}
	}

	return visited;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test("index.ts transitive graph does not import mongodb or node:* modules", () => {
	const entry = resolve(srcDir, "index.ts");
	const graph = buildImportGraph(entry);

	// Should include the entry and its transitive local imports
	assert.ok(graph.has(resolve(srcDir, "schema.ts")), "graph should include schema.ts");
	assert.ok(graph.has(resolve(srcDir, "errors.ts")), "graph should include errors.ts");
	assert.ok(graph.has(resolve(srcDir, "sanitize.ts")), "graph should include sanitize.ts");
	assert.ok(graph.has(resolve(srcDir, "api.ts")), "graph should include api.ts");
	assert.ok(graph.has(resolve(srcDir, "client.ts")), "graph should include client.ts");

	// Check each file in the graph
	for (const file of graph) {
		const { hasMongoDB, hasNodeBuiltin, nodeModules } = hasServerOnlyImport(file);
		assert.equal(hasMongoDB, false, `Expected ${file} to NOT import mongodb`);
		assert.equal(hasNodeBuiltin, false, `Expected ${file} to NOT import node:* modules, found: ${nodeModules.join(", ")}`);
	}
});

test("client.ts transitive graph does not import mongodb or node:* modules", () => {
	const entry = resolve(srcDir, "client.ts");
	const graph = buildImportGraph(entry);

	// Should include client and its dependencies
	assert.ok(graph.has(resolve(srcDir, "client.ts")), "graph should include client.ts");
	assert.ok(graph.has(resolve(srcDir, "schema.ts")), "graph should include schema.ts");
	assert.ok(graph.has(resolve(srcDir, "errors.ts")), "graph should include errors.ts");
	assert.ok(graph.has(resolve(srcDir, "sanitize.ts")), "graph should include sanitize.ts");
	assert.ok(graph.has(resolve(srcDir, "api.ts")), "graph should include api.ts");

	// client.ts graph must NOT include server.ts, permission.ts, or config.ts
	assert.equal(graph.has(resolve(srcDir, "server.ts")), false, "client graph must NOT include server.ts");
	assert.equal(graph.has(resolve(srcDir, "permission.ts")), false, "client graph must NOT include permission.ts");
	assert.equal(graph.has(resolve(srcDir, "config.ts")), false, "client graph must NOT include config.ts");

	// Check each file in the graph
	for (const file of graph) {
		const { hasMongoDB, hasNodeBuiltin, nodeModules } = hasServerOnlyImport(file);
		assert.equal(hasMongoDB, false, `Expected ${file} to NOT import mongodb`);
		assert.equal(hasNodeBuiltin, false, `Expected ${file} to NOT import node:* modules, found: ${nodeModules.join(", ")}`);
	}
});

test("server.ts DOES import mongodb (reverse check: proves the guard works)", () => {
	const serverFile = resolve(srcDir, "server.ts");
	const { hasMongoDB, hasNodeBuiltin, nodeModules } = hasServerOnlyImport(serverFile);

	assert.equal(hasMongoDB, true, "server.ts MUST import mongodb — if this fails the guard is not detecting it");
	assert.equal(hasNodeBuiltin, true, "server.ts MUST import node:* modules — if this fails the guard is not detecting them");
	assert.ok(nodeModules.includes("node:crypto"), "server.ts should import node:crypto");
	assert.ok(nodeModules.includes("node:http"), "server.ts should import node:http");
});
