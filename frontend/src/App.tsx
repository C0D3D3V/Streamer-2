import { useEffect } from "react";
import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { authApi } from "./api/auth";
import { apiFetch } from "./api/client";
import { useAuthStore } from "./store/authStore";
import { NavBar } from "./components/NavBar";
import { ProtectedRoute } from "./components/ProtectedRoute";
import SetupWizard from "./pages/SetupWizard";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import StreamerPage from "./pages/Streamer";
import ViewerPage from "./pages/Viewer";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1 },
  },
});

/**
 * AppShell loads setup status and the current user on mount.
 * - If setup is not complete, redirects to /setup.
 * - If setup is complete, loads the session user.
 */
function AppShell() {
  const { setUser, setLoading, setSetupComplete } = useAuthStore();
  const navigate = useNavigate();

  useEffect(() => {
    apiFetch<{ setup_complete: boolean; setup_locked: boolean }>("/api/setup/status")
      .then((res) => {
        setSetupComplete(res.setup_complete);
        if (!res.setup_complete) {
          navigate("/setup", { replace: true });
          setLoading(false);
          return;
        }
        return authApi.me().then(setUser).catch(() => setUser(null));
      })
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, [setUser, setLoading, setSetupComplete, navigate]);

  return null;
}

function ConditionalNavBar() {
  const location = useLocation();
  if (location.pathname.startsWith("/stream/")) return null;
  return <NavBar />;
}

/**
 * SetupRoute wraps the /setup page.
 * Once setup is complete (or locked), it redirects away to the dashboard.
 */
function SetupRoute() {
  const setupComplete = useAuthStore((s) => s.setupComplete);
  // If we know setup is done, redirect away.
  if (setupComplete === true) return <Navigate to="/" replace />;
  return <SetupWizard />;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AppShell />
        <ConditionalNavBar />
        <main>
          <Routes>
            {/* Public routes */}
            <Route path="/setup" element={<SetupRoute />} />
            <Route path="/login" element={<Login />} />
            <Route path="/watch/:token" element={<ViewerPage />} />

            {/* Protected routes */}
            <Route
              path="/"
              element={
                <ProtectedRoute>
                  <Dashboard />
                </ProtectedRoute>
              }
            />
            <Route
              path="/stream/:id"
              element={
                <ProtectedRoute>
                  <StreamerPage />
                </ProtectedRoute>
              }
            />
            {/* Fallback */}
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
