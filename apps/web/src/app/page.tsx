import { ControlSidebar } from "@/components/ControlSidebar";
import { PostgresDashboard } from "@/components/PostgresDashboard";
import { ServicesGrid } from "@/components/ServicesGrid";

export default function OverviewPage() {
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">
          Overview
        </h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
          Live status of every service and database in the stack, pulled
          directly from Prometheus.
        </p>
      </div>

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="flex flex-1 flex-col gap-6">
          <ServicesGrid />
          <PostgresDashboard />
        </div>
        <ControlSidebar />
      </div>
    </div>
  );
}
