"use client";

import { useEffect, useRef } from "react";
import type { ServerEvent } from "./api";

/** Subscribes to the API's SSE stream; reconnects automatically. */
export function useServerEvents(onEvent: (e: ServerEvent) => void) {
  const handler = useRef(onEvent);
  handler.current = onEvent;

  useEffect(() => {
    let es: EventSource | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout>;
    const connect = () => {
      es = new EventSource("/api/events", { withCredentials: true });
      es.onmessage = (m) => {
        try {
          handler.current(JSON.parse(m.data) as ServerEvent);
        } catch {
          /* ignore malformed */
        }
      };
      es.onerror = () => {
        es?.close();
        if (!closed) retry = setTimeout(connect, 3000);
      };
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(retry);
      es?.close();
    };
  }, []);
}
