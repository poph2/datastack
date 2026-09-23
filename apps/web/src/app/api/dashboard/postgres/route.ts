import { NextResponse } from "next/server";
import { promQuery, promValue } from "@/lib/prometheus";

export type PostgresDatabase = {
  database: string;
  connections: number;
  sizeBytes: number;
};

export async function GET() {
  try {
    const [connections, sizes] = await Promise.all([
      promQuery("pg_stat_database_numbackends"),
      promQuery("pg_database_size_bytes"),
    ]);

    const byDatabase = new Map<string, PostgresDatabase>();

    for (const sample of connections) {
      const database = sample.metric.datname;
      if (!database) continue;
      byDatabase.set(database, {
        database,
        connections: promValue(sample),
        sizeBytes: 0,
      });
    }

    for (const sample of sizes) {
      const database = sample.metric.datname;
      if (!database) continue;
      const existing = byDatabase.get(database) ?? {
        database,
        connections: 0,
        sizeBytes: 0,
      };
      existing.sizeBytes = promValue(sample);
      byDatabase.set(database, existing);
    }

    const databases = Array.from(byDatabase.values()).sort((a, b) =>
      a.database.localeCompare(b.database),
    );
    return NextResponse.json(databases);
  } catch {
    return NextResponse.json(
      { error: "could not reach prometheus" },
      { status: 502 },
    );
  }
}
