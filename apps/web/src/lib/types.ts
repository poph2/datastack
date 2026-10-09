// Shapes shared by the route handlers and the browser. These mirror the
// analyzer and chaos API contracts; the route handlers relay bodies as-is.

export type ApiError = {
  error: string;
  message: string;
  sql?: string;
};

// Analyzer

export type ModelsResponse = {
  provider: string;
  default: string;
  models: string[];
};

export type SchemaColumn = { name: string; type: string; comment: string };
export type SchemaTable = { name: string; comment: string; columns: SchemaColumn[] };
export type SchemaResponse = {
  tables: SchemaTable[];
  joins: { from: string; to: string }[];
  loaded_at: string;
};

export type Cell = string | number | boolean | null;

export type AskResponse = {
  question: string;
  model: string;
  sql: string;
  columns: { name: string; type: string }[];
  rows: Cell[][];
  row_count: number;
  truncated: boolean;
  attempts: number;
  duration_ms: number;
  trace_id: string;
};

// Chaos

export type ChaosKnob = {
  name: string;
  description: string;
  value: number;
  default: number;
  min: number;
  max: number;
};

export type ChaosState = { service: string; knobs: ChaosKnob[] };

export type DrainResponse = { drained: number; product_ids: (string | number)[] };

// Load (k6)

export const RATE_MIN = 1;
export const RATE_MAX = 100;

export type LoadState =
  | { available: false; reason: string }
  | { available: true; exists: false; deployment: string }
  | {
      available: true;
      exists: true;
      deployment: string;
      rate: number | null;
      replicas: number;
      readyReplicas: number;
      updatedReplicas: number;
      image: string | null;
    };
