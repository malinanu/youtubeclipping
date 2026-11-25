"use client";

import { useEffect, useRef, useState } from "react";
import type { ServerEvent } from "./api";

/**
 * Subscribes to the API's SSE stream; reconnects automatically.
 * Returns `live`: true while hello/ping events are actually arriving. When a
 * proxy buffers the stream, `live` stays false and pages fall back to polling.
 */
export function useServerEvents(onEvent: (e: ServerEvent) => void): boolean {
  const handler = useRef(onEvent);
  handler.current = onEvent;
  const [live, setLive] = useState(false);

  useEffect(() => {
    let es: EventSource | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout>;
    let lastSeen = 0;
    const seen = () => {
      lastSeen = Date.now();
      setLive(true);
    };
    // Server pings every 10s; if nothing arrives for 25s, treat as not live.
    const watchdog = setInterval(() => {
      if (Date.now() - lastSeen > 25_000) setLive(false);
    }, 5_000);
    const connect = () => {
      es = new EventSource("/api/events", { withCredentials: true });
      es.addEventListener("hello", seen);
      es.addEventListener("ping", seen);
      es.onmessage = (m) => {
        seen();
        try {
          handler.current(JSON.parse(m.data) as ServerEvent);
        } catch {
          /* ignore malformed */
        }
      };
      es.onerror = () => {
        es?.close();
        setLive(false);
        if (!closed) retry = setTimeout(connect, 3000);
      };
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(retry);
      clearInterval(watchdog);
      es?.close();
    };
  }, []);

  return live;
}

/** Calls fn every `ms` while `active` (used as a fallback when SSE isn't live). */
export function usePoll(active: boolean, ms: number, fn: () => void) {
  const f = useRef(fn);
  f.current = fn;
  useEffect(() => {
    if (!active) return;
    const iv = setInterval(() => f.current(), ms);
    return () => clearInterval(iv);
  }, [active, ms]);
}
