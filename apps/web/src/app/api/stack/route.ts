import { getWorkloadHealth, KubeUnavailableError } from "@/lib/k8s";
import { WATCHED_WORKLOADS } from "@/lib/stack";
import { NextResponse } from "next/server";

/**
 * Live status for the stack landscape's dots. Read-only, and deliberately
 * forgiving: if Kubernetes isn't reachable the page is still a perfectly good
 * showcase, so this answers 200 with an empty map rather than an error.
 */
export async function GET() {
  try {
    const health = await getWorkloadHealth(WATCHED_WORKLOADS);
    return NextResponse.json({ available: true, workloads: health });
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err);
    return NextResponse.json({
      available: false,
      reason: err instanceof KubeUnavailableError ? reason : `Kubernetes error: ${reason}`,
      workloads: {},
    });
  }
}
