import { createNextHandler, Permissions } from "@kequnyang/dopdb/server";
import { schema } from "@/dopdb-schema";

const perms = new Permissions()
  .grant("HGET", "users")
  .grant("HSET", "users")
  .grant("FIND", "users");

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  kvrocks: { uri: process.env.KVROCKS_URI!, namespace: "appdb" },
  jwtSecret: process.env.JWT_SECRET!,
  permissions: perms,
});

export const runtime = "nodejs";
