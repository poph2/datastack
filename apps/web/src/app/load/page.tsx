import { LoadPanel } from "@/components/load/LoadPanel";
import { PageHeader } from "@/components/ui";

export default function LoadPage() {
  return (
    <>
      <PageHeader
        title="Load"
        subtitle="k6 in the cluster runs customer journeys (sign up, ad click, checkout) at a steady rate."
      />
      <LoadPanel />
    </>
  );
}
