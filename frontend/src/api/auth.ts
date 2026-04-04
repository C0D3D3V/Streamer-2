import { apiFetch } from "./client";

export interface Me {
  subject: string;
  email: string;
  name: string;
}

export const authApi = {
  me: () => apiFetch<Me>("/api/me"),
  logout: () => apiFetch<void>("/auth/logout", { method: "POST" }),
  /** Navigate to this URL to start the OIDC login flow. */
  loginUrl: "/auth/login",
};
