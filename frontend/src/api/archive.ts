import { apiFetch } from "./client";

export interface Archive {
  id: string;
  stream_id: string;
  title: string;
  file_size_bytes: number;
  duration_secs: number;
  finalized_at?: string;
  created_at: string;
}

export const archiveApi = {
  list: () => apiFetch<Archive[]>("/api/archive"),
  get: (id: string) => apiFetch<Archive>(`/api/archive/${id}`),
  delete: (id: string) =>
    apiFetch<void>(`/api/archive/${id}`, { method: "DELETE" }),
  batchDelete: (ids: string[]) =>
    apiFetch<void>("/api/archive/batch-delete", {
      method: "POST",
      body: JSON.stringify({ ids }),
    }),
};
