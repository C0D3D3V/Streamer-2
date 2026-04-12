import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { apiFetch } from "../../api/client";

interface SetupForm {
  issuer_url: string;
  client_id: string;
  client_secret: string;
  external_url: string;
}

export default function SetupWizard() {
  const navigate = useNavigate();

  useEffect(() => { document.title = "Setup – Streamer"; }, []);

  const [form, setForm] = useState<SetupForm>({
    issuer_url: "",
    client_id: "",
    client_secret: "",
    external_url: globalThis.location.origin,
  });
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [e.target.name]: e.target.value }));

  const handleSubmit = async (e: React.SyntheticEvent<HTMLFormElement>) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await apiFetch("/api/setup/complete", {
        method: "POST",
        body: JSON.stringify(form),
      });
      navigate("/login");
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Setup failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex-1 flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        {/* Logo / heading */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-full bg-blue-600/20 border border-blue-500/30 mb-4">
            <span className="w-3 h-3 rounded-full bg-blue-500 block" />
          </div>
          <h1 className="text-2xl font-semibold text-white">Welcome to Streamer</h1>
          <p className="text-gray-400 mt-2 text-sm">
            Connect your Authelia OIDC client to enable login. This only needs to be done once.
          </p>
        </div>

        <div className="bg-surface border border-surface-border rounded-xl p-6 shadow-xl">
          <form onSubmit={handleSubmit} className="space-y-4">
            <Field label="Authelia Issuer URL" hint="Base URL of your Authelia instance">
              <input
                name="issuer_url"
                type="url"
                placeholder="https://auth.example.com"
                value={form.issuer_url}
                onChange={handleChange}
                required
                className={inputClass}
              />
            </Field>

            <Field label="OIDC Client ID">
              <input
                name="client_id"
                placeholder="streamer"
                value={form.client_id}
                onChange={handleChange}
                required
                className={inputClass}
              />
            </Field>

            <Field label="OIDC Client Secret">
              <input
                name="client_secret"
                type="password"
                placeholder="••••••••••••••••"
                value={form.client_secret}
                onChange={handleChange}
                required
                className={inputClass}
              />
            </Field>

            <Field
              label="External URL (this app)"
              hint={
                <>
                  Redirect URI will be:{" "}
                  <code className="text-blue-400 text-xs">{form.external_url}/auth/callback</code>
                </>
              }
            >
              <input
                name="external_url"
                type="url"
                placeholder="https://stream.example.com"
                value={form.external_url}
                onChange={handleChange}
                required
                className={inputClass}
              />
            </Field>

            {error && (
              <p className="text-sm text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
                {error}
              </p>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="w-full py-2.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white font-medium transition-colors mt-2"
            >
              {submitting ? "Saving…" : "Complete Setup"}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: Readonly<{ label: string; hint?: React.ReactNode; children: React.ReactNode }>) {
  return (
    <div className="space-y-1.5">
      <label className="block text-sm font-medium text-gray-300">{label}</label>
      {children}
      {hint && <p className="text-xs text-gray-500">{hint}</p>}
    </div>
  );
}

const inputClass =
  "w-full px-3 py-2 rounded-lg bg-surface-deep border border-surface-border text-white placeholder-gray-600 text-sm focus:outline-none focus:border-blue-500 transition-colors";
