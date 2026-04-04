import { create } from "zustand";
import type { Me } from "../api/auth";

interface AuthState {
  user: Me | null;
  loading: boolean;
  /** null = not yet checked; true/false = result of /api/setup/status */
  setupComplete: boolean | null;
  setUser: (user: Me | null) => void;
  setLoading: (loading: boolean) => void;
  setSetupComplete: (v: boolean) => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  loading: true,
  setupComplete: null,
  setUser: (user) => set({ user }),
  setLoading: (loading) => set({ loading }),
  setSetupComplete: (v) => set({ setupComplete: v }),
}));
