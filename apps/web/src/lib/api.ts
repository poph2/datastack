import type { ApiError } from "./types";

/** A failed call to one of our route handlers, carrying the error body. */
export class ApiRequestError extends Error {
  constructor(
    readonly status: number,
    readonly body: ApiError,
  ) {
    super(body.message || body.error || `request failed with ${status}`);
  }
}

/** Browser-side JSON call to a Next route handler under /api. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      headers: init.body
        ? { "content-type": "application/json", ...init.headers }
        : init.headers,
      cache: "no-store",
    });
  } catch (err) {
    throw new ApiRequestError(0, {
      error: "NETWORK",
      message: err instanceof Error ? err.message : "network error",
    });
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    const error =
      body && typeof body === "object" && "error" in body
        ? (body as ApiError)
        : { error: "HTTP_" + res.status, message: `request failed with ${res.status}` };
    throw new ApiRequestError(res.status, error);
  }
  return body as T;
}
