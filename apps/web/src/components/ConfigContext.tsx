"use client";

import { createContext, useContext } from "react";

// Runtime configuration the browser needs, passed down from the server
// layout (which reads it from the environment at request time).
export type ClientConfig = { grafanaUrl: string };

const ConfigContext = createContext<ClientConfig>({
  grafanaUrl: "http://localhost:3000",
});

export function ConfigProvider({
  value,
  children,
}: {
  value: ClientConfig;
  children: React.ReactNode;
}) {
  return <ConfigContext.Provider value={value}>{children}</ConfigContext.Provider>;
}

export function useConfig(): ClientConfig {
  return useContext(ConfigContext);
}
