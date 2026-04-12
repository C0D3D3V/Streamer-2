import { useState, type ReactNode } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { linksApi, type SharedLink } from "../api/links";

interface Props {
  /** ID of the stream — used for both listing and creating (Dashboard). */
  readonly streamId?: string;
  /** ID of the archive — used for creating; combined with streamId for listing (Archive). */
  readonly archiveId?: string;
}

export function LinksPanel({ streamId, archiveId }: Props) {
  const queryClient = useQueryClient();
  // Include both IDs in the query key so the cache is unique per combination.
  const queryKey = ["links", streamId, archiveId];

  const { data: links = [], isLoading } = useQuery({
    queryKey,
    queryFn: () => linksApi.list({ stream_id: streamId, archive_id: archiveId }),
  });

  const deleteMutation = useMutation({
    mutationFn: linksApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey }),
  });

  const createMutation = useMutation({
    mutationFn: linksApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey });
      setFormOpen(false);
      setSlug("");
      setPassword("");
      setSlugError(null);
    },
  });

  const [formOpen,  setFormOpen]  = useState(false);
  const [slug,      setSlug]      = useState("");
  const [password,  setPassword]  = useState("");
  const [slugError, setSlugError] = useState<string | null>(null);
  const [copied,    setCopied]    = useState<string | null>(null);

  const origin = globalThis.location.origin;

  const validateSlug = (v: string) => {
    if (!v) return null;
    if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$/.test(v))
      return "Lowercase letters, numbers and hyphens only. Must start and end with a letter or number.";
    if (v.length > 64) return "Max 64 characters.";
    return null;
  };

  const handleSlugChange = (v: string) => {
    setSlug(v);
    setSlugError(validateSlug(v));
  };

  const handleCreate = (e: React.SyntheticEvent<HTMLFormElement>) => {
    e.preventDefault();
    const ve = validateSlug(slug);
    if (ve) { setSlugError(ve); return; }
    createMutation.mutate({
      // For creation, archiveId takes priority (archive context); fall back to streamId.
      archive_id: archiveId,
      stream_id:  archiveId ? undefined : streamId,
      slug:       slug.trim().toLowerCase() || undefined,
      password:   password || undefined,
    });
  };

  const copyUrl = async (key: string, url: string) => {
    await navigator.clipboard.writeText(url);
    setCopied(key);
    setTimeout(() => setCopied((k) => (k === key ? null : k)), 2000);
  };

  const createError = createMutation.error;
  const createErrorMessage = createError?.message.toLowerCase().includes("taken")
    ? "That short name is already taken."
    : createError?.message;

  let linksContent: ReactNode;
  if (isLoading) {
    linksContent = (
      <div className="flex justify-center py-3">
        <div className="w-4 h-4 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
      </div>
    );
  } else if (links.length === 0) {
    linksContent = <p className="text-xs text-gray-600 py-1">No links yet.</p>;
  } else {
    linksContent = (
      <div className="space-y-2">
        {links.map((link) => (
          <LinkRow
            key={link.id}
            link={link}
            origin={origin}
            copied={copied}
            onCopy={copyUrl}
            onDelete={() => deleteMutation.mutate(link.id)}
            deleting={deleteMutation.isPending && deleteMutation.variables === link.id}
          />
        ))}
      </div>
    );
  }

  return (
    <div className="mt-3 pt-3 border-t border-surface-border">
      {/* Header row */}
      <div className="flex items-center justify-between mb-3">
        <span className="text-xs font-medium text-gray-400">Share links</span>
        <button
          onClick={() => { setFormOpen((v) => !v); setSlug(""); setPassword(""); setSlugError(null); }}
          className="flex items-center gap-1 text-xs px-2.5 py-1 rounded-lg bg-blue-600 hover:bg-blue-500 text-white transition-colors"
        >
          <span className="text-sm leading-none">+</span> New link
        </button>
      </div>

      {/* Create form */}
      {formOpen && (
        <form onSubmit={handleCreate} className="mb-3 p-3 rounded-lg bg-surface-deep border border-surface-border space-y-2">
          <div>
            <div className="flex items-center bg-surface border border-surface-border rounded-lg overflow-hidden focus-within:border-blue-500 transition-colors">
              <span className="px-2 text-gray-600 text-xs select-none shrink-0">/watch/</span>
              <input
                value={slug}
                onChange={(e) => handleSlugChange(e.target.value.toLowerCase())}
                placeholder="custom-name (optional)"
                className="flex-1 bg-transparent py-1.5 pr-2 text-white text-xs placeholder-gray-600 focus:outline-none"
              />
            </div>
            {slugError && <p className="text-xs text-red-400 mt-1">{slugError}</p>}
          </div>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Password (optional)"
            className="w-full px-2 py-1.5 rounded-lg bg-surface border border-surface-border text-white text-xs placeholder-gray-600 focus:outline-none focus:border-blue-500 transition-colors"
          />
          {createError && (
            <p className="text-xs text-red-400">{createErrorMessage}</p>
          )}
          <div className="flex gap-2 justify-end">
            <button
              type="button"
              onClick={() => setFormOpen(false)}
              className="text-xs px-3 py-1.5 rounded-lg border border-surface-border text-gray-400 hover:text-white transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={createMutation.isPending || !!slugError}
              className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white transition-colors"
            >
              {createMutation.isPending ? "Creating…" : "Create"}
            </button>
          </div>
        </form>
      )}

      {/* Links list */}
      {linksContent}
    </div>
  );
}

function LinkRow({
  link, origin, copied, onCopy, onDelete, deleting,
}: Readonly<{
  link: SharedLink;
  origin: string;
  copied: string | null;
  onCopy: (key: string, url: string) => void;
  onDelete: () => void;
  deleting: boolean;
}>) {
  // If the link has a slug, show only the slug URL (shorter and more memorable).
  // Only fall back to the token URL when no slug is set.
  const displayUrl = link.slug
    ? `${origin}/watch/${link.slug}`
    : `${origin}/watch/${link.token}`;

  return (
    <div className="rounded-lg bg-surface-deep border border-surface-border px-3 py-2 space-y-1.5">
      <div className="flex items-center gap-2">
        <span className="flex-1 text-xs text-gray-400 font-mono truncate min-w-0">{displayUrl}</span>
        <button
          onClick={() => onCopy(link.id, displayUrl)}
          className="shrink-0 text-xs px-2 py-0.5 rounded bg-surface-border hover:bg-surface-border-hover text-gray-300 hover:text-white transition-colors"
        >
          {copied === link.id ? "✓" : "Copy"}
        </button>
      </div>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {link.has_password && (
            <span className="text-xs text-gray-600 flex items-center gap-1">
              <svg xmlns="http://www.w3.org/2000/svg" className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
              </svg>
              Password protected
            </span>
          )}
          {link.stream_id && !link.archive_id && (
            <span className="text-xs text-gray-600">stream link</span>
          )}
        </div>
        {!link.is_default && (
          <button
            onClick={onDelete}
            disabled={deleting}
            className="text-xs text-red-400/60 hover:text-red-400 disabled:opacity-40 transition-colors"
          >
            {deleting ? "Deleting…" : "Delete"}
          </button>
        )}
      </div>
    </div>
  );
}
