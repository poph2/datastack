import { readFile } from "node:fs/promises";
import https from "node:https";
import { k6Deployment, k8sNamespace } from "./config";
import type { LoadState } from "./types";

// A tiny Kubernetes API client for the one thing the UI needs: reading and
// patching the k6 Deployment.
//
// In the cluster it uses the pod's ServiceAccount token and CA. For local
// development, point K8S_API_URL at `kubectl --context kind-datastack proxy`
// (http://127.0.0.1:8001), which handles authentication itself. Without
// either, the load page reports that it can't reach Kubernetes.

const SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount";

export class KubeUnavailableError extends Error {}

export type KubeResponse = { status: number; body: unknown };

type Transport = (
  method: string,
  path: string,
  body?: unknown,
  contentType?: string,
) => Promise<KubeResponse>;

async function transport(): Promise<Transport> {
  const proxyUrl = process.env.K8S_API_URL?.replace(/\/+$/, "");
  if (proxyUrl) {
    return async (method, path, body, contentType) => {
      const res = await fetch(proxyUrl + path, {
        method,
        headers: body ? { "content-type": contentType ?? "application/json" } : {},
        body: body ? JSON.stringify(body) : undefined,
        cache: "no-store",
        signal: AbortSignal.timeout(10_000),
      });
      return { status: res.status, body: await res.json().catch(() => null) };
    };
  }

  let token: string;
  let ca: Buffer;
  try {
    [token, ca] = await Promise.all([
      readFile(`${SA_DIR}/token`, "utf8"),
      readFile(`${SA_DIR}/ca.crt`),
    ]);
  } catch {
    throw new KubeUnavailableError(
      "Not running in the cluster (no ServiceAccount token). Set K8S_API_URL to a `kubectl proxy` for local development.",
    );
  }
  const host = process.env.KUBERNETES_SERVICE_HOST || "kubernetes.default.svc";
  const port = Number(process.env.KUBERNETES_SERVICE_PORT || 443);

  return (method, path, body, contentType) =>
    new Promise((resolve, reject) => {
      const payload = body ? JSON.stringify(body) : undefined;
      const req = https.request(
        {
          host,
          port,
          path,
          method,
          ca,
          timeout: 10_000,
          headers: {
            authorization: `Bearer ${token.trim()}`,
            accept: "application/json",
            ...(payload
              ? {
                  "content-type": contentType ?? "application/json",
                  "content-length": Buffer.byteLength(payload),
                }
              : {}),
          },
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on("data", (c: Buffer) => chunks.push(c));
          res.on("end", () => {
            const text = Buffer.concat(chunks).toString("utf8");
            let parsed: unknown = null;
            try {
              parsed = JSON.parse(text);
            } catch {
              parsed = text;
            }
            resolve({ status: res.statusCode ?? 0, body: parsed });
          });
        },
      );
      req.on("timeout", () => req.destroy(new Error("Kubernetes API timed out")));
      req.on("error", reject);
      if (payload) req.write(payload);
      req.end();
    });
}

type Deployment = {
  spec?: {
    replicas?: number;
    template?: {
      spec?: {
        containers?: {
          name: string;
          image?: string;
          env?: { name: string; value?: string }[];
        }[];
      };
    };
  };
  status?: { readyReplicas?: number; updatedReplicas?: number };
};

function deploymentPath(): string {
  return `/apis/apps/v1/namespaces/${encodeURIComponent(k8sNamespace())}/deployments/${encodeURIComponent(k6Deployment())}`;
}

function toState(d: Deployment): LoadState {
  const containers = d.spec?.template?.spec?.containers ?? [];
  const container = containers.find((c) => c.name === "k6") ?? containers[0];
  const raw = container?.env?.find((e) => e.name === "RATE")?.value;
  const rate = raw !== undefined && raw !== "" ? Number(raw) : null;
  return {
    available: true,
    exists: true,
    deployment: k6Deployment(),
    rate: rate !== null && Number.isFinite(rate) ? rate : null,
    replicas: d.spec?.replicas ?? 0,
    readyReplicas: d.status?.readyReplicas ?? 0,
    updatedReplicas: d.status?.updatedReplicas ?? 0,
    image: container?.image ?? null,
  };
}

function kubeError(res: KubeResponse): Error {
  const message =
    typeof res.body === "object" && res.body && "message" in res.body
      ? String((res.body as { message: unknown }).message)
      : `Kubernetes API answered ${res.status}`;
  return new Error(message);
}

export async function getLoad(): Promise<LoadState> {
  let send: Transport;
  try {
    send = await transport();
  } catch (err) {
    if (err instanceof KubeUnavailableError) {
      return { available: false, reason: err.message };
    }
    throw err;
  }
  const res = await send("GET", deploymentPath());
  if (res.status === 404) {
    return { available: true, exists: false, deployment: k6Deployment() };
  }
  if (res.status !== 200) throw kubeError(res);
  return toState(res.body as Deployment);
}

/**
 * Strategic-merge patch of the k6 Deployment: the RATE env var (merged into
 * the container's env list by name) and/or spec.replicas. Changing RATE rolls
 * the pod, which is how k6 picks up the new rate.
 */
export async function patchLoad(change: {
  rate?: number;
  replicas?: number;
}): Promise<LoadState> {
  const send = await transport();
  const spec: Record<string, unknown> = {};
  if (change.replicas !== undefined) spec.replicas = change.replicas;
  if (change.rate !== undefined) {
    spec.template = {
      spec: {
        containers: [
          { name: "k6", env: [{ name: "RATE", value: String(change.rate) }] },
        ],
      },
    };
  }
  const res = await send(
    "PATCH",
    deploymentPath(),
    { spec },
    "application/strategic-merge-patch+json",
  );
  if (res.status === 404) {
    return { available: true, exists: false, deployment: k6Deployment() };
  }
  if (res.status !== 200) throw kubeError(res);
  return toState(res.body as Deployment);
}
