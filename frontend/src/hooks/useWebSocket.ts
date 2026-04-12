import { useEffect, useLayoutEffect, useRef } from "react";

export interface WSMessage {
  type: string;
  payload: Record<string, unknown>;
}

/**
 * Opens a WebSocket connection to /ws/stream/:streamId and calls onMessage
 * for each received event. Automatically reconnects on disconnect.
 */
export function useWebSocket(
  streamId: string | undefined,
  onMessage: (msg: WSMessage) => void,
  options?: { query?: string },
) {
  const wsRef = useRef<WebSocket | null>(null);
  const onMessageRef = useRef(onMessage);
  useLayoutEffect(() => {
    onMessageRef.current = onMessage;
  });

  useEffect(() => {
    if (!streamId) return;

    const connect = () => {
      const protocol = globalThis.location.protocol === "https:" ? "wss" : "ws";
      const url = `${protocol}://${globalThis.location.host}/ws/stream/${streamId}${options?.query ? `?${options.query}` : ""}`;
      const ws = new WebSocket(url);
      wsRef.current = ws;

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as WSMessage;
          onMessageRef.current(msg);
        } catch {
          // Ignore malformed messages.
        }
      };

      ws.onclose = () => {
        // Reconnect after 3 seconds on unexpected close.
        setTimeout(connect, 3000);
      };
    };

    connect();
    return () => {
      wsRef.current?.close();
    };
  }, [streamId]);
}
