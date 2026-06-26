// Typed errors — one hierarchy, used on both sides.
//
// On the server these are thrown by db/api operations; Serve() maps each to an
// HTTP status. On the client the remote caller reverses the mapping: a non-2xx
// response is turned back into the matching class, so front-end code branches
// with `instanceof` instead of string-matching `dopdb: 403 ...`.
//
//   try { await db.Order.hget(id); }
//   catch (e) {
//     if (e instanceof ForbiddenError) ...
//     if (e instanceof NotFoundError)  ...
//   }

/** Base class for every dopdb error. `status` is the HTTP status it maps to. */
export class DopdbError extends Error {
  readonly status: number;
  /** Machine-readable code, stable across client/server (e.g. "forbidden"). */
  readonly code: string;
  constructor(message: string, status: number, code: string) {
    super(message);
    this.name = new.target.name;
    this.status = status;
    this.code = code;
    // restore prototype chain across the transpile/target boundary
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

/** 400 — input failed schema validation. Carries the per-field failures. */
export class ValidationError extends DopdbError {
  readonly fields: FieldIssue[];
  constructor(fields: FieldIssue[], message?: string) {
    super(message ?? `validation failed: ${fields.map((f) => f.field).join(", ")}`, 400, "validation");
    this.fields = fields;
  }
}

export interface FieldIssue {
  field: string;
  message: string;
}

/** 401 — authentication required or token invalid. */
export class UnauthorizedError extends DopdbError {
  constructor(message = "authentication required") {
    super(message, 401, "unauthorized");
  }
}

/** 403 — row-level ownership check failed (document owned by someone else). */
export class ForbiddenError extends DopdbError {
  constructor(message = "forbidden") {
    super(message, 403, "forbidden");
  }
}

/** 404 — no document for the given key / no matching endpoint. */
export class NotFoundError extends DopdbError {
  constructor(message = "not found") {
    super(message, 404, "not_found");
  }
}

/** 409 — uniqueness conflict (unique index, or HSETNX on an existing key). */
export class ConflictError extends DopdbError {
  constructor(message = "conflict") {
    super(message, 409, "conflict");
  }
}

const byCode: Record<string, (msg: string) => DopdbError> = {
  validation: (m) => new ValidationError([], m),
  unauthorized: (m) => new UnauthorizedError(m),
  forbidden: (m) => new ForbiddenError(m),
  not_found: (m) => new NotFoundError(m),
  conflict: (m) => new ConflictError(m),
};

const byStatus: Record<number, (msg: string) => DopdbError> = {
  400: (m) => new ValidationError([], m),
  401: (m) => new UnauthorizedError(m),
  403: (m) => new ForbiddenError(m),
  404: (m) => new NotFoundError(m),
  409: (m) => new ConflictError(m),
};

/** Reconstruct a typed error from an HTTP response (client side). */
export function errorFromResponse(status: number, body: unknown): DopdbError {
  let code = "";
  let message = `dopdb: ${status}`;
  if (body && typeof body === "object") {
    const b = body as Record<string, unknown>;
    if (typeof b.code === "string") code = b.code;
    if (typeof b.error === "string") message = b.error;
    else if (typeof b.message === "string") message = b.message;
  } else if (typeof body === "string" && body) {
    message = body;
  }
  if (code && byCode[code]) return byCode[code](message);
  if (byStatus[status]) return byStatus[status](message);
  return new DopdbError(message, status || 500, code || "error");
}
