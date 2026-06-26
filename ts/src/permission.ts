// permission.ts — the command/collection permission gate, equivalent to Go's
// httpserve.Permissions. Keyed by "COMMAND::collection". Default is DENY: a pair
// that was never granted is refused. There is no AutoAuth (grant-on-first-use);
// grants are always explicit.
//
//   const perms = new Permissions();
//   perms.grant("HGET", "User").grant("HSET", "User");
//   serve({ schema, mongo, jwtSecret, permissions: perms });

import { readFile, writeFile } from "node:fs/promises";

export class Permissions {
  private m = new Map<string, boolean>();

  private static key(cmd: string, coll: string): string {
    return `${cmd.toUpperCase()}::${coll}`;
  }

  /** Reports whether (cmd, coll) is explicitly permitted. Unknown pairs → false. */
  allowed(cmd: string, coll: string): boolean {
    return this.m.get(Permissions.key(cmd, coll)) === true;
  }

  /** Add an allow entry. Chainable. */
  grant(cmd: string, coll: string): this {
    this.m.set(Permissions.key(cmd, coll), true);
    return this;
  }

  /** Add an explicit deny entry (beats nothing in particular; absence already
   * denies — kept for parity with the Go API and to override a prior grant). */
  deny(cmd: string, coll: string): this {
    this.m.set(Permissions.key(cmd, coll), false);
    return this;
  }

  /** Plain-object view of the permission map (for serialization/inspection). */
  toJSON(): Record<string, boolean> {
    return Object.fromEntries(this.m);
  }

  /** Persist the permission map to a JSON file. */
  async saveJSON(path: string): Promise<void> {
    await writeFile(path, JSON.stringify(this.toJSON(), null, 2), "utf8");
  }

  /** Load a permission map previously written with saveJSON. */
  static async loadJSON(path: string): Promise<Permissions> {
    const data = await readFile(path, "utf8");
    const obj = JSON.parse(data) as Record<string, boolean>;
    const p = new Permissions();
    for (const [k, v] of Object.entries(obj)) p.m.set(k, v === true);
    return p;
  }
}
