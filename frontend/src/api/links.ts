import { apiFetch } from "./client";

export interface SharedLink {
  id: string;
  token: string;
  slug?: string;
  stream_id?: string;
  archive_id?: string;
  has_password: boolean;
  is_default: boolean;
  expires_at?: string;
  created_at: string;
}

export interface WatchInfo {
  requires_password?: boolean;
  stream_title?: string;
  stream_id?: string;
  archive_id?: string;
  stream_status?: string;
  stream_quality?: string;
  stream_scheduled_at?: string;
  stream_rotation?: number;
  dash_url?: string;
  hls_url?: string;
  archive_url?: string;
}

export const linksApi = {
  list: (params?: { stream_id?: string; archive_id?: string }) => {
    const q = new URLSearchParams();
    if (params?.stream_id) q.set("stream_id", params.stream_id);
    if (params?.archive_id) q.set("archive_id", params.archive_id);
    const qs = q.toString();
    const path = qs ? `/api/links?${qs}` : "/api/links";
    return apiFetch<SharedLink[]>(path);
  },

  create: (data: {
    stream_id?: string;
    archive_id?: string;
    slug?: string;
    password?: string;
    expires_at?: string;
  }) =>
    apiFetch<SharedLink>("/api/links", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  /** Returns the single default/most-recent link for a stream or archive. */
  for: (params: { stream_id?: string; archive_id?: string }) => {
    const q = new URLSearchParams();
    if (params.stream_id) q.set("stream_id", params.stream_id);
    if (params.archive_id) q.set("archive_id", params.archive_id);
    return apiFetch<SharedLink>(`/api/links/for?${q.toString()}`);
  },

  delete: (id: string) =>
    apiFetch<void>(`/api/links/${id}`, { method: "DELETE" }),

  watchInfo: (token: string) =>
    apiFetch<WatchInfo>(`/api/watch/${token}/info`),

  watchAuth: (token: string, password: string) =>
    apiFetch<WatchInfo>(`/api/watch/${token}/auth`, {
      method: "POST",
      body: JSON.stringify({ password }),
    }),
};
