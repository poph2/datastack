import { analyzerUrl } from "@/lib/config";
import { forward } from "@/lib/upstream";
import { NextResponse } from "next/server";

// A local LLM can take a couple of minutes (generation plus a retry after a
// failed query), so this handler waits far longer than the others. The
// timeout sits above the analyzer's own LLM deadline, so the analyzer's
// error (LLM_UNAVAILABLE and so on) reaches the page instead of ours.
export const maxDuration = 300;

const ASK_TIMEOUT_MS = 280_000;

export async function POST(request: Request) {
  const body = (await request.json().catch(() => null)) as {
    question?: unknown;
    model?: unknown;
  } | null;
  const question = typeof body?.question === "string" ? body.question.trim() : "";
  if (!question) {
    return NextResponse.json(
      { error: "INVALID_REQUEST", message: "question is required" },
      { status: 400 },
    );
  }
  const model = typeof body?.model === "string" && body.model ? body.model : undefined;
  return forward(`${analyzerUrl()}/ask`, {
    method: "POST",
    body: JSON.stringify(model ? { question, model } : { question }),
    timeoutMs: ASK_TIMEOUT_MS,
  });
}
