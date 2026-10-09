import { ConfigProvider } from "@/components/ConfigContext";
import { Sidebar } from "@/components/Sidebar";
import { grafanaUrl } from "@/lib/config";
import type { Metadata } from "next";
import { connection } from "next/server";
import "./globals.css";

export const metadata: Metadata = {
  title: "DataStack control center",
  description: "Ask the data, inject chaos and drive load on the DataStack demo",
};

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // Render per request, so GRAFANA_URL is read at runtime, not build time.
  await connection();
  const config = { grafanaUrl: grafanaUrl() };

  return (
    <html lang="en" className="h-full antialiased">
      <body className="min-h-full font-sans">
        <ConfigProvider value={config}>
          <div className="flex min-h-screen flex-col lg:flex-row">
            <Sidebar />
            <main className="min-w-0 flex-1 px-5 py-6 lg:px-10 lg:py-8">
              <div className="mx-auto max-w-6xl">{children}</div>
            </main>
          </div>
        </ConfigProvider>
      </body>
    </html>
  );
}
