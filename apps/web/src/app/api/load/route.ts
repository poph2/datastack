import { getLoad, KubeUnavailableError, patchLoad } from "@/lib/k8s";
import { RATE_MAX, RATE_MIN } from "@/lib/types";
import { NextResponse } from "next/server";

function invalid(message: string) {
  return NextResponse.json({ error: "INVALID_REQUEST", message }, { status: 400 });
}

function failure(err: unknown) {
  const unavailable = err instanceof KubeUnavailableError;
  return NextResponse.json(
    {
      error: unavailable ? "KUBERNETES_UNAVAILABLE" : "KUBERNETES_ERROR",
      message: err instanceof Error ? err.message : String(err),
    },
    { status: unavailable ? 503 : 502 },
  );
}

export async function GET() {
  try {
    return NextResponse.json(await getLoad());
  } catch (err) {
    return failure(err);
  }
}

/** Body: {"rate"?: 1..100, "running"?: boolean}. */
export async function PUT(request: Request) {
  const body = (await request.json().catch(() => null)) as {
    rate?: unknown;
    running?: unknown;
  } | null;
  if (!body || (body.rate === undefined && body.running === undefined)) {
    return invalid("send rate and/or running");
  }

  const change: { rate?: number; replicas?: number } = {};
  if (body.rate !== undefined) {
    const rate = Number(body.rate);
    if (!Number.isInteger(rate) || rate < RATE_MIN || rate > RATE_MAX) {
      return invalid(`rate must be an integer from ${RATE_MIN} to ${RATE_MAX}`);
    }
    change.rate = rate;
  }
  if (body.running !== undefined) {
    if (typeof body.running !== "boolean") return invalid("running must be a boolean");
    change.replicas = body.running ? 1 : 0;
  }

  try {
    const state = await patchLoad(change);
    if (state.available && !state.exists) {
      return NextResponse.json(
        { error: "NOT_FOUND", message: "the k6 Deployment does not exist" },
        { status: 404 },
      );
    }
    return NextResponse.json(state);
  } catch (err) {
    return failure(err);
  }
}
