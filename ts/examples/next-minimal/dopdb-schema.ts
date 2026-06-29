import { collection, f } from "@kequnyang/dopdb";

export const schema = {
  users: collection({
    name: f.string(),
    email: f.string().unique(),
    age: f.number().optional(),
    owner: f.string(),
  })
    .named("users")
    .ownerScope("owner"),
};
