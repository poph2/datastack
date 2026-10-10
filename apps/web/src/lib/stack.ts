// The tools DataStack is built from, grouped by the architecture layer they
// serve (docs/microservices/README.md). This is the data behind the /stack
// landscape page.
//
// Logos live in public/logos/ (see the README there for provenance). Two
// render modes: "mono" logos are single-path marks with no fill of their own,
// so the page paints them with `brand` through a CSS mask; "color" logos carry
// their own artwork and are drawn as-is.

export const LAYERS = [
  {
    id: "operational",
    name: "Operational",
    blurb: "Six Go services, one database each",
  },
  { id: "load", name: "Load", blurb: "Simulated customers" },
  { id: "analytical", name: "Analytical", blurb: "Every change, seconds later" },
  { id: "observability", name: "Observability", blurb: "Traces, metrics, dashboards" },
  { id: "insight", name: "Insight", blurb: "Plain English to SQL" },
  { id: "platform", name: "Platform", blurb: "What it all runs on" },
] as const;

export type LayerId = (typeof LAYERS)[number]["id"];

export type Tool = {
  id: string;
  name: string;
  /** One line: what it does *here*, not what it is in general. */
  role: string;
  layer: LayerId;
  /** Headline tools show by default; libraries appear behind the toggle. */
  library?: boolean;
  logo: string;
  mode: "mono" | "color";
  /** Brand colour. Used to paint "mono" logos and to tint the tile. */
  brand: string;
  /** Overrides the monogram derived from the name, when that reads badly. */
  initials?: string;
  /** Kubernetes workload this tool runs as, if any: drives the live dot. */
  workload?: string;
  href: string;
};

export const TOOLS: Tool[] = [
  // Operational
  {
    id: "go",
    name: "Go",
    role: "All six services, the CDC daemon and the analyzer",
    layer: "operational",
    logo: "go.svg",
    mode: "mono",
    brand: "#00ADD8",
    href: "https://go.dev",
  },
  {
    id: "postgresql",
    name: "PostgreSQL",
    role: "One database per service, the system of record",
    layer: "operational",
    logo: "postgresql.svg",
    mode: "mono",
    brand: "#4169E1",
    workload: "datastack-pg-1",
    href: "https://www.postgresql.org",
  },
  {
    id: "cloudnativepg",
    name: "CloudNativePG",
    role: "Runs and heals the Postgres cluster",
    layer: "operational",
    logo: "cloudnativepg.svg",
    mode: "color",
    brand: "#3E8AC8",
    href: "https://cloudnative-pg.io",
  },
  {
    id: "gin",
    name: "Gin",
    role: "HTTP router behind every service",
    layer: "operational",
    library: true,
    logo: "gin.svg",
    mode: "mono",
    brand: "#008ECF",
    href: "https://gin-gonic.com",
  },
  {
    id: "gorm",
    name: "GORM",
    role: "The models are the schema: AutoMigrate and the column comments",
    layer: "operational",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#B2DFDB",
    href: "https://gorm.io",
  },
  {
    id: "pgx",
    name: "pgx",
    role: "Postgres driver under GORM",
    layer: "operational",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#4169E1",
    href: "https://github.com/jackc/pgx",
  },
  {
    id: "cobra",
    name: "Cobra + Viper",
    role: "Every flag is also an environment variable",
    layer: "operational",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#59C9A5",
    initials: "CV",
    href: "https://cobra.dev",
  },
  {
    id: "decimal",
    name: "shopspring/decimal",
    role: "Money, never float64",
    layer: "operational",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#86EFAC",
    href: "https://github.com/shopspring/decimal",
  },
  {
    id: "testcontainers",
    name: "Testcontainers",
    role: "Repository tests against a real Postgres",
    layer: "operational",
    library: true,
    logo: "",
    mode: "mono",
    // Their mark is near-black; lifted so it reads on the dark tile.
    brand: "#A78BFA",
    initials: "TC",
    href: "https://testcontainers.com",
  },

  // Load
  {
    id: "k6",
    name: "k6",
    role: "Sign up, ad click, checkout — at a rate you set",
    layer: "load",
    logo: "k6.svg",
    mode: "mono",
    brand: "#7D64FF",
    workload: "k6",
    href: "https://k6.io",
  },

  // Analytical
  {
    id: "starrocks",
    name: "StarRocks",
    role: "The analytics warehouse every dashboard queries",
    layer: "analytical",
    logo: "starrocks.svg",
    mode: "color",
    brand: "#00B0FF",
    workload: "starrocks-0",
    href: "https://www.starrocks.io",
  },
  {
    id: "cdc",
    name: "CDC sync",
    role: "Polls (updated_at, id) and Stream Loads micro-batches",
    layer: "analytical",
    logo: "",
    mode: "mono",
    brand: "#38BDF8",
    workload: "cdc-sync-service",
    href: "https://github.com/poph2/datastack",
  },

  // Observability
  {
    id: "opentelemetry",
    name: "OpenTelemetry",
    role: "One trace across six services",
    layer: "observability",
    logo: "opentelemetry.svg",
    mode: "mono",
    brand: "#F5A800",
    workload: "otel-collector",
    href: "https://opentelemetry.io",
  },
  {
    id: "tempo",
    name: "Grafana Tempo",
    role: "Stores the traces, generates the service graph",
    layer: "observability",
    logo: "tempo.svg",
    mode: "color",
    brand: "#F46800",
    workload: "tempo-0",
    href: "https://grafana.com/oss/tempo/",
  },
  {
    id: "prometheus",
    name: "Prometheus",
    role: "Scrapes every service and the CDC daemon",
    layer: "observability",
    logo: "prometheus.svg",
    mode: "mono",
    brand: "#E6522C",
    workload: "prometheus",
    href: "https://prometheus.io",
  },
  {
    id: "grafana",
    name: "Grafana",
    role: "Seven dashboards over Prometheus, Tempo and StarRocks",
    layer: "observability",
    logo: "grafana.svg",
    mode: "mono",
    brand: "#F46800",
    workload: "grafana",
    href: "https://grafana.com",
  },

  // Insight
  {
    id: "ollama",
    name: "Ollama",
    role: "Runs the model on the GPU, outside the cluster",
    layer: "insight",
    logo: "ollama.svg",
    mode: "mono",
    brand: "#E5E7EB",
    href: "https://ollama.com",
  },
  {
    id: "qwen",
    name: "qwen2.5-coder",
    role: "Writes the SQL. 20/20 on the evaluation set",
    layer: "insight",
    logo: "",
    mode: "mono",
    brand: "#A78BFA",
    href: "https://ollama.com/library/qwen2.5-coder",
  },
  {
    id: "claude",
    name: "Claude",
    role: "Optional hosted provider for the analyzer",
    layer: "insight",
    logo: "claude.svg",
    mode: "mono",
    brand: "#D97757",
    href: "https://www.anthropic.com",
  },
  {
    id: "nextjs",
    name: "Next.js",
    role: "This control center",
    layer: "insight",
    logo: "nextdotjs.svg",
    mode: "mono",
    brand: "#E5E7EB",
    workload: "web",
    href: "https://nextjs.org",
  },
  {
    id: "react",
    name: "React",
    role: "The UI components",
    layer: "insight",
    library: true,
    logo: "react.svg",
    mode: "mono",
    brand: "#61DAFB",
    href: "https://react.dev",
  },
  {
    id: "typescript",
    name: "TypeScript",
    role: "The web app, end to end",
    layer: "insight",
    library: true,
    logo: "typescript.svg",
    mode: "mono",
    brand: "#3178C6",
    href: "https://www.typescriptlang.org",
  },
  {
    id: "tailwind",
    name: "Tailwind CSS",
    role: "Styling, readable from the back of the room",
    layer: "insight",
    library: true,
    logo: "tailwindcss.svg",
    mode: "mono",
    brand: "#06B6D4",
    href: "https://tailwindcss.com",
  },
  {
    id: "recharts",
    name: "Recharts",
    role: "The bar chart on an answer",
    layer: "insight",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#22D3EE",
    href: "https://recharts.org",
  },

  // Platform
  {
    id: "kubernetes",
    name: "Kubernetes",
    role: "Everything here is a workload on one node",
    layer: "platform",
    logo: "kubernetes.svg",
    mode: "mono",
    brand: "#326CE5",
    href: "https://kubernetes.io",
  },
  {
    id: "kind",
    name: "kind",
    role: "The whole cluster, in Docker, on this laptop",
    layer: "platform",
    logo: "kind.svg",
    mode: "color",
    brand: "#326CE5",
    href: "https://kind.sigs.k8s.io",
  },
  {
    id: "helm",
    name: "Helm",
    role: "One generic chart deploys all eight Go services",
    layer: "platform",
    logo: "helm.svg",
    mode: "mono",
    brand: "#90A6F0",
    href: "https://helm.sh",
  },
  {
    id: "docker",
    name: "Docker",
    role: "One Dockerfile builds every service",
    layer: "platform",
    logo: "docker.svg",
    mode: "mono",
    brand: "#2496ED",
    href: "https://www.docker.com",
  },
  {
    id: "distroless",
    name: "distroless",
    role: "No shell, no package manager, non-root",
    layer: "platform",
    library: true,
    logo: "",
    mode: "mono",
    brand: "#94A3B8",
    href: "https://github.com/GoogleContainerTools/distroless",
  },
  {
    id: "node",
    name: "Node.js",
    role: "Serves the control center",
    layer: "platform",
    library: true,
    logo: "nodedotjs.svg",
    mode: "mono",
    brand: "#5FA04E",
    href: "https://nodejs.org",
  },
];

export function toolsForLayer(layer: LayerId, withLibraries: boolean): Tool[] {
  return TOOLS.filter((t) => t.layer === layer && (withLibraries || !t.library));
}

/** Workloads the live dots ask about, deduplicated. */
export const WATCHED_WORKLOADS = Array.from(
  new Set(TOOLS.flatMap((t) => (t.workload ? [t.workload] : []))),
);
