import { useEffect, useRef, useCallback } from "react";

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
) {
  const wsRef = useRef<WebSocket | null>(null);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage; // always call the latest callback

  const connect = useCallback(() => {
    if (!streamId) return;

    const protocol = window.location.protocol === "https:" ? "wss" : "ws";
    const url = `${protocol}://${window.location.host}/ws/stream/${streamId}`;
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
  }, [streamId]);

  useEffect(() => {
    connect();
    return () => {
      wsRef.current?.close();
    };
  }, [connect]);
}
