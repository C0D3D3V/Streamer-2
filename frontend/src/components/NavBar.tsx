import { Link, useNavigate } from "react-router-dom";
import { useAuthStore } from "../store/authStore";
import { authApi } from "../api/auth";

export function NavBar() {
  const { user, setUser } = useAuthStore();
  const navigate = useNavigate();

  const handleLogout = async () => {
    await authApi.logout();
    setUser(null);
    navigate("/login");
  };

  return (
    <nav className="border-b border-surface-border bg-surface px-6 py-3 flex items-center justify-between sticky top-0 z-50">
      <Link to="/" className="flex items-center gap-2 font-semibold text-white text-lg hover:text-blue-400 transition-colors">
        <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" />{" "}
        Streamer
      </Link>
      <div className="flex items-center gap-4">
        {user ? (
          <>
            <Link to="/" className="text-sm text-gray-400 hover:text-white transition-colors">Dashboard</Link>
            <span className="text-gray-600 text-sm">{user.email}</span>
            <button
              onClick={handleLogout}
              className="text-sm px-3 py-1.5 rounded-md border border-surface-border text-gray-400 hover:text-white hover:border-gray-500 transition-colors"
            >
              Logout
            </button>
          </>
        ) : (
          <Link to="/login" className="text-sm px-3 py-1.5 rounded-md bg-blue-600 hover:bg-blue-500 text-white transition-colors">
            Login
          </Link>
        )}
      </div>
    </nav>
  );
}
