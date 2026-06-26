// Type-level tests. Not executed by the runner (no ".test.ts"); verified by
// `tsc --noEmit`. If inference regresses, the build fails here.

import { f, collection, type Infer, type InferInput } from "../src/schema.js";
import { clientDb } from "../src/client.js";

type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
type Expect<T extends true> = T;

const User = collection({
  _id: f.string(),
  name: f.string(),
  age: f.number().optional(),
  role: f.string().default("user"), // server-filled
  owner: f.string().bind("@uid"), // server-filled
});

// ---- Infer: the stored/returned shape (optionals optional; server fields present) ----
type UserOut = Infer<typeof User>;
type _out = Expect<
  Equal<
    UserOut,
    { _id: string; name: string; role: string; owner: string; age?: number }
  >
>;

// ---- InferInput: server-filled fields (default/bind) become optional; the
//      key field _id is optional too (it is the method argument) ----
type UserIn = InferInput<typeof User>;
type _in = Expect<
  Equal<
    UserIn,
    { name: string; _id?: string; age?: number; role?: string; owner?: string }
  >
>;

// ---- the client surface is correctly typed ----
const schema = { User };
const db = clientDb(schema, {});

async function checks() {
  const u = await db.User.hget("u1");
  type _null = Expect<Equal<typeof u, Infer<typeof User> | null>>;

  // input type is InferInput: owner/role optional, name required
  await db.User.hset("u1", { _id: "u1", name: "Ada" });
  await db.User.hset("u1", { _id: "u1", name: "Ada", age: 3, role: "admin" });

  // @ts-expect-error — name is required on input
  await db.User.hset("u1", { _id: "u1", age: 3 });

  // @ts-expect-error — age must be a number
  await db.User.hset("u1", { _id: "u1", name: "Ada", age: "old" });

  // @ts-expect-error — unknown field
  await db.User.hset("u1", { _id: "u1", name: "Ada", nope: true });
}

void checks;
