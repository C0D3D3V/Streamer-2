import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { streamsApi, type Stream } from "../../api/streams";
import { archiveApi, type Archive } from "../../api/archive";
import { linksApi } from "../../api/links";
import { LinksPanel } from "../../components/LinksPanel";
import { ConfirmDialog } from "../../components/ConfirmDialog";

const STATUS_COLORS: Record<string, string> = {
  scheduled: "bg-yellow-500/15 text-yellow-400 border-yellow-500/30",
  live:       "bg-red-500/15 text-red-400 border-red-500/30",
  failed:     "bg-red-900/30 text-red-500 border-red-700/30",
};

type Item =
  | { kind: "stream";  data: Stream;  sortKey: number }
  | { kind: "archive"; data: Archive; sortKey: number };

export default function Dashboard() {
  const queryClient = useQueryClient();

  useEffect(() => { document.title = "Dashboard – Streamer"; }, []);

  const { data: streams  = [], isLoading: streamsLoading  } = useQuery({ queryKey: ["streams"],  queryFn: streamsApi.list });
  const { data: archives = [], isLoading: archivesLoading } = useQuery({ queryKey: ["archive"], queryFn: archiveApi.list });

  const [title,       setTitle]       = useState("");
  const [scheduledAt, setScheduledAt] = useState("");
  const [createOpen,  setCreateOpen]  = useState(false);
  const [expandedLinks, setExpandedLinks] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget]   = useState<Item | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const createMutation = useMutation({
    mutationFn: streamsApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["streams"] });
      setTitle(""); setScheduledAt(""); setCreateOpen(false);
    },
  });

  const deleteStreamMutation = useMutation({
    mutationFn: streamsApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["streams"] }),
  });

  const deleteArchiveMutation = useMutation({
    mutationFn: archiveApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["archive"] }),
  });

  const batchDeleteStreamsMutation = useMutation({
    mutationFn: streamsApi.batchDelete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["streams"] });
      setSelected(new Set());
    },
  });

  const batchDeleteArchivesMutation = useMutation({
    mutationFn: archiveApi.batchDelete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["archive"] });
      setSelected(new Set());
    },
  });

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault();
    const scheduled_at = scheduledAt ? new Date(scheduledAt).toISOString() : undefined;
    createMutation.mutate({ title, scheduled_at });
  };

  const confirmDelete = () => {
    if (!deleteTarget) return;
    if (deleteTarget.kind === "stream")  deleteStreamMutation.mutate(deleteTarget.data.id);
    if (deleteTarget.kind === "archive") deleteArchiveMutation.mutate(deleteTarget.data.id);
    setDeleteTarget(null);
  };

  const toggleLinks = (id: string) =>
    setExpandedLinks((cur) => (cur === id ? null : id));

  const toggleSelection = (id: string) => {
    const newSelected = new Set(selected);
    if (newSelected.has(id)) {
      newSelected.delete(id);
    } else {
      newSelected.add(id);
    }
    setSelected(newSelected);
  };

  const toggleSelectAll = () => {
    if (selected.size === items.length) {
      setSelected(new Set());
    } else {
      setSelected(new Set(items.map((item) => item.data.id)));
    }
  };

  const handleBatchDelete = () => {
    const streamIds = Array.from(selected).filter((id) =>
      items.find((item) => item.data.id === id && item.kind === "stream")
    );
    const archiveIds = Array.from(selected).filter((id) =>
      items.find((item) => item.data.id === id && item.kind === "archive")
    );

    if (streamIds.length > 0) {
      batchDeleteStreamsMutation.mutate(streamIds);
    }
    if (archiveIds.length > 0) {
      batchDeleteArchivesMutation.mutate(archiveIds);
    }
  };

  const isLoading = streamsLoading || archivesLoading;

  // Only show streams that are NOT ended — ended streams are represented by their archive entry.
  const activeStreams: Item[] = streams
    .filter((s) => s.status !== "ended")
    .map((s) => ({ kind: "stream", data: s, sortKey: new Date(s.created_at).getTime() }));

  const archiveItems: Item[] = archives
    .map((a) => ({ kind: "archive", data: a, sortKey: new Date(a.created_at).getTime() }));

  const items: Item[] = [...activeStreams, ...archiveItems]
    .sort((a, b) => b.sortKey - a.sortKey);

  const deleteLabel = deleteTarget?.kind === "stream"
    ? `Delete stream "${(deleteTarget.data as Stream).title}"? This cannot be undone.`
    : `Delete recording "${(deleteTarget?.data as Archive | undefined)?.title}"? This cannot be undone.`;

  return (
    <div className="flex-1 p-4 sm:p-6 max-w-4xl mx-auto w-full">
      {deleteTarget && (
        <ConfirmDialog
          title={deleteTarget.kind === "stream" ? "Delete stream" : "Delete recording"}
          message={deleteLabel}
          onConfirm={confirmDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}

      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-white">Dashboard</h1>
        <div className="flex items-center gap-2 flex-wrap justify-end">
          {selected.size > 0 && (
            <>
              <span className="text-sm text-gray-400">{selected.size} selected</span>
              <button
                onClick={toggleSelectAll}
                className="text-xs px-3 py-2 rounded-lg border border-gray-500 text-gray-400 hover:text-white hover:border-gray-400 transition-colors"
              >
                {selected.size === items.length ? "Deselect All" : "Select All"}
              </button>
              <button
                onClick={handleBatchDelete}
                disabled={batchDeleteStreamsMutation.isPending || batchDeleteArchivesMutation.isPending}
                className="text-xs px-3 py-2 rounded-lg border border-red-500/30 text-red-400 hover:bg-red-500/10 disabled:opacity-50 transition-colors"
              >
                {batchDeleteStreamsMutation.isPending || batchDeleteArchivesMutation.isPending
                  ? "Deleting…"
                  : "Delete Selected"}
              </button>
            </>
          )}
          {selected.size === 0 && (
            <button
              onClick={() => setCreateOpen((v) => !v)}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium transition-colors"
            >
              <span className="text-lg leading-none">+</span> New Stream
            </button>
          )}
        </div>
      </div>

      {/* Create form */}
      {createOpen && (
        <div className="bg-[#1a1b23] border border-[#2e3042] rounded-xl p-5 mb-6">
          <h2 className="text-sm font-medium text-gray-300 mb-4">Schedule a Stream</h2>
          <form onSubmit={handleCreate} className="flex flex-wrap gap-3 items-end">
            <div className="flex-1 min-w-48 space-y-1">
              <label className="text-xs text-gray-500">Title</label>
              <input
                placeholder="My Stream"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                required
                className={inputCls}
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-gray-500">Schedule (optional)</label>
              <input type="datetime-local" value={scheduledAt} onChange={(e) => setScheduledAt(e.target.value)} className={inputCls} />
            </div>
            <button
              type="submit"
              disabled={createMutation.isPending}
              className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white text-sm font-medium transition-colors"
            >
              {createMutation.isPending ? "Creating…" : "Create"}
            </button>
          </form>
        </div>
      )}

      {/* Combined list */}
      {isLoading ? (
        <div className="flex justify-center py-16">
          <div className="w-6 h-6 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
        </div>
      ) : items.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="text-4xl mb-3">📡</p>
          <p>No streams yet. Create one to get started.</p>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((item) =>
            item.kind === "stream"
              ? <StreamCard
                  key={item.data.id}
                  stream={item.data as Stream}
                  linksOpen={expandedLinks === item.data.id}
                  onToggleLinks={() => toggleLinks(item.data.id)}
                  onDelete={() => setDeleteTarget(item)}
                  isSelected={selected.has(item.data.id)}
                  onToggleSelection={() => toggleSelection(item.data.id)}
                />
              : <ArchiveCard
                  key={item.data.id}
                  archive={item.data as Archive}
                  linksOpen={expandedLinks === item.data.id}
                  onToggleLinks={() => toggleLinks(item.data.id)}
                  onDelete={() => setDeleteTarget(item)}
                  isSelected={selected.has(item.data.id)}
                  onToggleSelection={() => toggleSelection(item.data.id)}
                />
          )}
        </div>
      )}
    </div>
  );
}

function StreamCard({ stream: s, linksOpen, onToggleLinks, onDelete, isSelected, onToggleSelection }: {
  stream: Stream;
  linksOpen: boolean;
  onToggleLinks: () => void;
  onDelete: () => void;
  isSelected: boolean;
  onToggleSelection: () => void;
}) {
  const { data: watchLink } = useQuery({
    queryKey: ["links", "for", s.id],
    queryFn: () => linksApi.for({ stream_id: s.id }),
    enabled: s.status === "live" || s.status === "ended",
  });

  return (
    <div className={`bg-[#1a1b23] border rounded-xl p-4 transition-colors ${
      isSelected ? "border-blue-500/50 bg-blue-500/5" : "border-[#2e3042]"
    }`}>
      <div className="flex items-start gap-3">
        <input
          type="checkbox"
          checked={isSelected}
          onChange={onToggleSelection}
          className="w-4 h-4 mt-1 rounded accent-blue-600 cursor-pointer shrink-0"
        />
        <div className="flex-1 min-w-0">
          {/* Title + badges */}
          <div className="flex items-center gap-2 flex-wrap">
            {s.status === "live" && (
              <span className="relative flex h-2.5 w-2.5 shrink-0">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-red-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2.5 w-2.5 bg-red-500" />
              </span>
            )}
            <p className="text-white font-medium">{s.title}</p>
            <span className={`text-xs px-2 py-0.5 rounded-full border ${STATUS_COLORS[s.status] ?? ""}`}>{s.status}</span>
            <span className="text-xs text-gray-600 bg-[#13141a] px-2 py-0.5 rounded">{s.tier || "–"}</span>
          </div>
          {s.scheduled_at && s.status === "scheduled" && (
            <p className="text-xs text-gray-500 mt-0.5">Scheduled: {new Date(s.scheduled_at).toLocaleString()}</p>
          )}
          {/* Actions */}
          <div className="flex items-center gap-2 flex-wrap mt-2">
            {s.status === "scheduled" && (
              <Link to={`/stream/${s.id}`} className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white transition-colors">
                Go Live
              </Link>
            )}
            {watchLink && (
              <a
                href={`/watch/${watchLink.token}`}
                target="_blank"
                rel="noreferrer"
                className="text-xs px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white transition-colors"
              >
                Watch
              </a>
            )}
            <LinkToggleBtn open={linksOpen} onClick={onToggleLinks} />
            <button onClick={onDelete} className={deleteBtnCls}>Delete</button>
          </div>
        </div>
      </div>
      {linksOpen && <LinksPanel streamId={s.id} />}
    </div>
  );
}

function ArchiveCard({ archive: a, linksOpen, onToggleLinks, onDelete, isSelected, onToggleSelection }: {
  archive: Archive;
  linksOpen: boolean;
  onToggleLinks: () => void;
  onDelete: () => void;
  isSelected: boolean;
  onToggleSelection: () => void;
}) {
  const { data: watchLink } = useQuery({
    queryKey: ["links", "for", a.stream_id],
    queryFn: () => linksApi.for({ stream_id: a.stream_id }),
  });

  return (
    <div className={`bg-[#1a1b23] border rounded-xl p-4 transition-colors ${
      isSelected ? "border-blue-500/50 bg-blue-500/5" : "border-[#2e3042]"
    }`}>
      <div className="flex items-start gap-3">
        <input
          type="checkbox"
          checked={isSelected}
          onChange={onToggleSelection}
          className="w-4 h-4 mt-1 rounded accent-blue-600 cursor-pointer shrink-0"
        />
        <div className="flex-1 min-w-0">
          {/* Title + meta */}
          <p className="text-white font-medium">{a.title}</p>
          <div className="flex gap-2 mt-0.5 flex-wrap">
            <span className="text-xs text-gray-500">{fmtDuration(a.duration_secs)}</span>
            <span className="text-xs text-gray-600">·</span>
            <span className="text-xs text-gray-500">{fmtSize(a.file_size_bytes)}</span>
            {a.finalized_at && (
              <>
                <span className="text-xs text-gray-600">·</span>
                <span className="text-xs text-gray-500">{new Date(a.finalized_at).toLocaleDateString()}</span>
              </>
            )}
          </div>
          {/* Actions */}
          <div className="flex items-center gap-2 flex-wrap mt-2">
            <a
              href={watchLink ? `/watch/${watchLink.token}` : undefined}
              target="_blank"
              rel="noreferrer"
              className={`text-xs px-3 py-1.5 rounded-lg bg-blue-600 text-white transition-colors ${watchLink ? "hover:bg-blue-500" : "opacity-50 pointer-events-none"}`}
            >
              Watch
            </a>
            <LinkToggleBtn open={linksOpen} onClick={onToggleLinks} />
            <button onClick={onDelete} className={deleteBtnCls}>Delete</button>
          </div>
        </div>
      </div>
      {linksOpen && <LinksPanel archiveId={a.id} streamId={a.stream_id} />}
    </div>
  );
}

function LinkToggleBtn({ open, onClick }: { open: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={`text-xs px-3 py-1.5 rounded-lg border transition-colors ${
        open
          ? "border-blue-500/40 text-blue-400 bg-blue-500/10"
          : "border-[#2e3042] text-gray-400 hover:text-white hover:border-gray-500"
      }`}
    >
      Links
    </button>
  );
}

function fmtDuration(secs: number) {
  const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60), s = secs % 60;
  return [h, m, s].map((n) => String(n).padStart(2, "0")).join(":");
}

function fmtSize(bytes: number) {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3)   return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

const inputCls    = "px-3 py-2 rounded-lg bg-[#13141a] border border-[#2e3042] text-white text-sm placeholder-gray-600 focus:outline-none focus:border-blue-500 transition-colors";
const deleteBtnCls = "text-xs px-3 py-1.5 rounded-lg border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-colors";
