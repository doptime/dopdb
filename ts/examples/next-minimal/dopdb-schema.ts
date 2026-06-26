import { collection, f } from "dopdb";

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
