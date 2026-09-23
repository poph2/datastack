import { NextResponse } from "next/server";
import { promQuery, promValue } from "@/lib/prometheus";

export type ServiceStatus = {
  job: string;
  service: string;
  instance: string;
  up: boolean;
};

export async function GET() {
  try {
    const samples = await promQuery("up");
    const services: ServiceStatus[] = samples.map((sample) => ({
      job: sample.metric.job ?? "unknown",
      service: sample.metric.service ?? sample.metric.job ?? "unknown",
      instance: sample.metric.instance ?? "",
      up: promValue(sample) === 1,
    }));
    services.sort((a, b) => a.service.localeCompare(b.service));
    return NextResponse.json(services);
  } catch {
    return NextResponse.json(
      { error: "could not reach prometheus" },
      { status: 502 },
    );
  }
}
