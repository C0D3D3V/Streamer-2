import { apiFetch } from "./client";

export type Tier = "QHD" | "FHD" | "HD";
export type StreamStatus = "scheduled" | "live" | "ended" | "failed";

export interface Stream {
  id: string;
  title: string;
  status: StreamStatus;
  tier: Tier | "";
  aspect_ratio: number;
  scheduled_at?: string;
  started_at?: string;
  ended_at?: string;
  owner_id: string;
  rotation: number;
  created_at: string;
}

export const streamsApi = {
  list: () => apiFetch<Stream[]>("/api/streams"),

  get: (id: string) => apiFetch<Stream>(`/api/streams/${id}`),

  create: (data: {
    title: string;
    scheduled_at?: string;
  }) =>
    apiFetch<Stream>("/api/streams", {
      method: "POST",
      body: JSON.stringify(data),
    }),

  delete: (id: string) =>
    apiFetch<void>(`/api/streams/${id}`, { method: "DELETE" }),

  batchDelete: (ids: string[]) =>
    apiFetch<void>("/api/streams/batch-delete", {
      method: "POST",
      body: JSON.stringify({ ids }),
    }),

  start: (id: string, rotation: number, tier: Tier, aspectRatio: number) =>
    apiFetch<Stream>(`/api/streams/${id}/start`, {
      method: "POST",
      body: JSON.stringify({ rotation, tier, aspect_ratio: aspectRatio }),
    }),

  stop: (id: string) =>
    apiFetch<Stream>(`/api/streams/${id}/stop`, { method: "POST" }),
};
