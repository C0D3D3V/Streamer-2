import { Navigate } from "react-router-dom";
import { useAuthStore } from "../store/authStore";

interface Props {
  children: React.ReactNode;
}

export function ProtectedRoute({ children }: Props) {
  const { user, loading } = useAuthStore();

  if (loading) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  if (!user) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
