import { analyzerUrl } from "@/lib/config";
import { forward } from "@/lib/upstream";

export async function GET() {
  return forward(`${analyzerUrl()}/models`);
}
