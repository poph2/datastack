import { NextResponse } from "next/server";

// Forwards a request from a route handler to an in-cluster service and
// relays its status and JSON body. Network failures and timeouts become the
// same {"error", "message"} shape the Go services use, so the browser only
// ever handles one error format.

const NON_JSON_ERRORS: Record<number, string> = {
  404: "NOT_FOUND",
  405: "METHOD_NOT_ALLOWED",
};

export async function forward(
  url: string,
  init: RequestInit & { timeoutMs?: number } = {},
): Promise<NextResponse> {
  const { timeoutMs = 10_000, ...rest } = init;
  let res: Response;
  try {
    res = await fetch(url, {
      ...rest,
      headers: { "content-type": "application/json", ...rest.headers },
      cache: "no-store",
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch (err) {
    const timedOut = err instanceof Error && err.name === "TimeoutError";
    return NextResponse.json(
      {
        error: timedOut ? "UPSTREAM_TIMEOUT" : "UPSTREAM_UNAVAILABLE",
        message: timedOut
          ? `${url} did not answer within ${Math.round(timeoutMs / 1000)}s`
          : `could not reach ${url}: ${describe(err)}`,
      },
      { status: timedOut ? 504 : 502 },
    );
  }

  const text = await res.text();
  try {
    return NextResponse.json(JSON.parse(text), { status: res.status });
  } catch {
    if (res.ok) {
      return NextResponse.json({ raw: text }, { status: res.status });
    }
    return NextResponse.json(
      {
        error: NON_JSON_ERRORS[res.status] ?? "UPSTREAM_ERROR",
        message: text.trim() || `${url} answered ${res.status}`,
      },
      { status: res.status },
    );
  }
}

function describe(err: unknown): string {
  if (err instanceof Error) {
    const cause = (err as Error & { cause?: unknown }).cause;
    if (cause instanceof Error) return cause.message;
    return err.message;
  }
  return String(err);
}
