import { createNextHandler, Permissions } from "dopdb/server";
import { schema } from "@/dopdb-schema";

const perms = new Permissions()
  .grant("HGET", "users")
  .grant("HSET", "users")
  .grant("FIND", "users");

export const { GET, POST, OPTIONS } = createNextHandler({
  schema,
  mongo: { uri: process.env.MONGO_URI!, db: "appdb" },
  jwtSecret: process.env.JWT_SECRET!,
  permissions: perms,
});

export const runtime = "nodejs";
