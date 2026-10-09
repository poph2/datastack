import { NextResponse } from "next/server";

/** Liveness and readiness probe for the web Deployment. */
export function GET() {
  return NextResponse.json({ status: "ok" });
}
