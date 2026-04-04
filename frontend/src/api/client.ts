/**
 * Base API client. All fetch calls go through here so we have a single place
 * to handle auth errors, base URLs, and request configuration.
 */

const API_BASE = import.meta.env.VITE_API_BASE ?? "";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** Perform a JSON API request. Throws ApiError on non-2xx responses. */
export async function apiFetch<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    credentials: "include", // always send session cookies
    headers: {
      "Content-Type": "application/json",
      ...options.headers,
    },
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }

  // 204 No Content
  if (res.status === 204) return undefined as T;

  return res.json() as Promise<T>;
}

/** Send a raw binary body (used for HLS ingest chunks). */
export async function apiPostBinary(
  path: string,
  data: Blob | ArrayBuffer,
): Promise<void> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/octet-stream" },
    body: data,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
}
