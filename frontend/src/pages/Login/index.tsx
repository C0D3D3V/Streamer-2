import { useEffect } from "react";

export default function Login() {
  useEffect(() => { document.title = "Sign In – Streamer"; }, []);

  return (
    <div className="flex-1 flex items-center justify-center p-4">
      <div className="w-full max-w-sm text-center">
        <div className="inline-flex items-center justify-center w-14 h-14 rounded-full bg-blue-600/20 border border-blue-500/30 mb-6">
          <span className="w-4 h-4 rounded-full bg-blue-500 block" />
        </div>
        <h1 className="text-2xl font-semibold text-white mb-2">Sign In</h1>
        <p className="text-gray-400 text-sm mb-8">
          Sign in with your Authelia account to access the streamer.
        </p>
        <a
          href="/auth/login"
          className="inline-flex items-center gap-2 px-6 py-3 rounded-lg bg-blue-600 hover:bg-blue-500 text-white font-medium transition-colors shadow-lg shadow-blue-900/30"
        >
          Sign in with Authelia
        </a>
      </div>
    </div>
  );
}
