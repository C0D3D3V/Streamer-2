import { useQuery } from "@tanstack/react-query";
import { linksApi, type WatchInfo } from "../api/links";

/**
 * Polls the watch info endpoint every 5 seconds until the stream goes live.
 * Used on the viewer page when the stream is scheduled but not yet started.
 */
export function useStreamPolling(token: string, enabled: boolean) {
  return useQuery<WatchInfo>({
    queryKey: ["watch", token],
    queryFn: () => linksApi.watchInfo(token),
    // Keep polling while enabled (stream not live yet).
    refetchInterval: enabled ? 5000 : false,
    // Don't throw on 404/410 – show a friendly message instead.
    retry: false,
  });
}
