import { StackLandscape } from "@/components/stack/StackLandscape";
import { PageHeader } from "@/components/ui";

export default function StackPage() {
  return (
    <>
      <PageHeader
        title="The stack"
        subtitle="Every tool behind this demo, grouped by the layer it serves. A green dot means it is running in the cluster right now."
      />
      <StackLandscape />
    </>
  );
}
