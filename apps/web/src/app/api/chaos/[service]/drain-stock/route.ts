import { serviceUrl } from "@/lib/config";
import { forward } from "@/lib/upstream";
import { NextResponse } from "next/server";

/** Inventory only. Body: {"products": 1..100}. */
export async function POST(
  req: Request,
  ctx: RouteContext<"/api/chaos/[service]/drain-stock">,
) {
  const { service } = await ctx.params;
  if (service !== "inventory") {
    return NextResponse.json(
      { error: "NOT_FOUND", message: "drain-stock exists only on inventory" },
      { status: 404 },
    );
  }
  return forward(`${serviceUrl("inventory")}/admin/chaos/drain-stock`, {
    method: "POST",
    body: await req.text(),
    timeoutMs: 15_000,
  });
}
