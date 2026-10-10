import { createContext, useContext } from "react";
import type { SessionResponse } from "./generated-contracts";
export type Session = SessionResponse;
export interface Brand {
  id: number;
  name: string;
  from_name: string;
  from_email: string;
  reply_to?: string;
  tracking_domain?: string;
  configuration_set?: string;
}
export const AppContext = createContext<{
  brandId: number;
  brand: Brand | undefined;
  brands: Brand[];
  session: Session;
  logout: () => Promise<void>;
} | null>(null);
export function useApp() {
  const value = useContext(AppContext);
  if (!value) throw new Error("App context unavailable");
  return value;
}
