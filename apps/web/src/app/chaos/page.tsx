import { ChaosBoard } from "@/components/chaos/ChaosBoard";
import { SERVICES } from "@/lib/config";

export default function ChaosPage() {
  return <ChaosBoard services={SERVICES} />;
}
