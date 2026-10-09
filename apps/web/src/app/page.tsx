import { AskPanel } from "@/components/ask/AskPanel";
import { PageHeader } from "@/components/ui";

export default function AskPage() {
  return (
    <>
      <PageHeader
        title="Ask the data"
        subtitle="A local LLM turns your question into SQL and runs it on StarRocks, which CDC keeps a few seconds behind the six service databases."
      />
      <AskPanel />
    </>
  );
}
