"use client";

import { useState } from "react";
import { backendUrl } from "@/lib/config";

type Message = {
  role: "user" | "agent";
  text: string;
  sql?: string;
};

export default function AgentPage() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);

  async function send() {
    const question = input.trim();
    if (!question || sending) return;

    setMessages((prev) => [...prev, { role: "user", text: question }]);
    setInput("");
    setSending(true);

    try {
      const res = await fetch(`${backendUrl()}/api/agent/ask`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ question }),
      });
      if (!res.ok) throw new Error(`status ${res.status}`);
      const data: { question: string; sql?: string } = await res.json();
      setMessages((prev) => [
        ...prev,
        {
          role: "agent",
          text: data.sql
            ? "Here's what I found:"
            : "The agent isn't wired up to Ollama/ClickHouse yet — this is a shell response.",
          sql: data.sql,
        },
      ]);
    } catch {
      setMessages((prev) => [
        ...prev,
        { role: "agent", text: "Could not reach the backend agent API." },
      ]);
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">
          Agent
        </h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
          Ask questions about revenue and fulfillment. Generated SQL is always
          shown alongside the answer.
        </p>
      </div>

      <div className="grid gap-4 lg:grid-cols-5">
        <div className="flex h-[550px] flex-col rounded-lg border border-zinc-200 bg-white shadow-sm dark:border-zinc-800 dark:bg-zinc-900 lg:col-span-3">
          <div className="flex-1 space-y-3 overflow-y-auto p-4">
            {messages.length === 0 && (
              <p className="text-sm text-zinc-400 dark:text-zinc-500">
                Ask something like &ldquo;What was revenue by customer segment
                last week?&rdquo;
              </p>
            )}
            {messages.map((message, i) => (
              <div
                key={i}
                className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
                  message.role === "user"
                    ? "ml-auto bg-zinc-900 text-white dark:bg-zinc-50 dark:text-zinc-900"
                    : "bg-zinc-100 text-zinc-800 dark:bg-zinc-800 dark:text-zinc-200"
                }`}
              >
                <p>{message.text}</p>
                {message.sql && (
                  <pre className="mt-2 overflow-x-auto rounded bg-black/80 p-2 text-xs text-emerald-300">
                    {message.sql}
                  </pre>
                )}
              </div>
            ))}
          </div>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              send();
            }}
            className="flex gap-2 border-t border-zinc-200 p-3 dark:border-zinc-800"
          >
            <input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="Ask a question about the analytics data…"
              className="flex-1 rounded-md border border-zinc-300 bg-white px-3 py-2 text-sm text-zinc-900 outline-none focus:border-zinc-500 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-100"
            />
            <button
              type="submit"
              disabled={sending}
              className="rounded-md bg-zinc-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-zinc-700 disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-900 dark:hover:bg-zinc-200"
            >
              {sending ? "Asking…" : "Ask"}
            </button>
          </form>
        </div>

        <div className="flex h-[550px] flex-col items-center justify-center rounded-lg border border-dashed border-zinc-300 bg-white text-center dark:border-zinc-700 dark:bg-zinc-900 lg:col-span-2">
          <p className="px-6 text-sm text-zinc-400 dark:text-zinc-500">
            Dashboards and charts the agent creates in Metabase will render
            here.
          </p>
        </div>
      </div>
    </div>
  );
}
