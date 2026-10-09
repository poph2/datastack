import { isServiceName, serviceUrl } from "@/lib/config";
import { forward } from "@/lib/upstream";
import { NextResponse } from "next/server";

type Ctx = RouteContext<"/api/chaos/[service]">;

const TIMEOUT_MS = 5_000;

/** The service's /admin/chaos URL, or a 404 response for an unknown name. */
async function chaosUrl(ctx: Ctx): Promise<string | NextResponse> {
  const { service } = await ctx.params;
  if (!isServiceName(service)) {
    return NextResponse.json(
      { error: "NOT_FOUND", message: `unknown service ${service}` },
      { status: 404 },
    );
  }
  return `${serviceUrl(service)}/admin/chaos`;
}

export async function GET(_req: Request, ctx: Ctx) {
  const url = await chaosUrl(ctx);
  if (typeof url !== "string") return url;
  return forward(url, { timeoutMs: TIMEOUT_MS });
}

/** Partial update, e.g. {"latency_ms": 500}. */
export async function PUT(req: Request, ctx: Ctx) {
  const url = await chaosUrl(ctx);
  if (typeof url !== "string") return url;
  return forward(url, { method: "PUT", body: await req.text(), timeoutMs: TIMEOUT_MS });
}

/** Resets every knob to its default. */
export async function DELETE(_req: Request, ctx: Ctx) {
  const url = await chaosUrl(ctx);
  if (typeof url !== "string") return url;
  return forward(url, { method: "DELETE", timeoutMs: TIMEOUT_MS });
}
