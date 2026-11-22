"use client";

import { useRef } from "react";
import type { Clip, Word } from "@/lib/api";
import { cn, fmtTime } from "@/lib/util";

/** Full-length overview: clips, pending selection and playhead. Click to seek. */
export function Timeline({
  durationMs,
  t,
  clips,
  selectedId,
  selection,
  chapters,
  onSeek,
  onSelectClip,
}: {
  durationMs: number;
  t: number;
  clips: Clip[];
  selectedId: string | null;
  selection: { s: number; e: number } | null;
  chapters?: { start_time: number; title: string }[];
  onSeek: (ms: number) => void;
  onSelectClip: (id: string) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const pct = (ms: number) => `${(Math.min(Math.max(ms, 0), durationMs) / Math.max(durationMs, 1)) * 100}%`;
  return (
    <div className="space-y-1">
      <div
        ref={ref}
        className="relative h-10 cursor-pointer rounded-md bg-panel2"
        onClick={(e) => {
          const r = ref.current!.getBoundingClientRect();
          onSeek(((e.clientX - r.left) / r.width) * durationMs);
        }}
      >
        {chapters?.map((c, i) => (
          <div key={i} className="absolute top-0 h-full border-l border-white/10" style={{ left: pct(c.start_time * 1000) }} title={c.title} />
        ))}
        {selection && (
          <div className="absolute top-0 h-full bg-sky-500/30" style={{ left: pct(selection.s), width: `calc(${pct(selection.e)} - ${pct(selection.s)})` }} />
        )}
        {clips.map((c) => (
          <div
            key={c.id}
            onClick={(e) => {
              e.stopPropagation();
              onSelectClip(c.id);
            }}
            className={cn(
              "absolute top-1.5 h-7 min-w-[3px] rounded-sm",
              c.id === selectedId ? "bg-accent ring-2 ring-white/70" : c.source === "ai" ? "bg-accent/60 hover:bg-accent/80" : "bg-emerald-500/60 hover:bg-emerald-500/80",
            )}
            style={{ left: pct(c.startMs), width: `calc(${pct(c.endMs)} - ${pct(c.startMs)})` }}
            title={`${c.title || "Untitled"} (${fmtTime(c.startMs)}–${fmtTime(c.endMs)})`}
          />
        ))}
        <div className="pointer-events-none absolute top-0 h-full w-0.5 bg-white" style={{ left: pct(t) }} />
      </div>
      <div className="flex justify-between text-[11px] text-muted">
        <span>0:00</span>
        <span>{fmtTime(t, true)}</span>
        <span>{fmtTime(durationMs)}</span>
      </div>
    </div>
  );
}

/** Zoomed trimmer around one clip with draggable in/out handles and word ticks. */
export function Trimmer({
  startMs,
  endMs,
  durationMs,
  t,
  words,
  onChange,
  onSeek,
}: {
  startMs: number;
  endMs: number;
  durationMs: number;
  t: number;
  words: Word[];
  onChange: (s: number, e: number) => void;
  onSeek: (ms: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const pad = Math.max(5000, (endMs - startMs) * 0.25);
  const winS = Math.max(0, startMs - pad);
  const winE = Math.min(durationMs || endMs + pad, endMs + pad);
  const span = Math.max(1, winE - winS);
  const x = (ms: number) => ((ms - winS) / span) * 100;
  const toMs = (clientX: number) => {
    const r = ref.current!.getBoundingClientRect();
    return Math.round(winS + ((clientX - r.left) / r.width) * span);
  };
  const dragging = useRef<"s" | "e" | null>(null);

  const visible = words.filter((w) => w.e > winS && w.s < winE);
  return (
    <div
      ref={ref}
      className="relative h-14 select-none overflow-hidden rounded-md bg-panel2"
      onPointerMove={(e) => {
        if (!dragging.current) return;
        const ms = toMs(e.clientX);
        if (dragging.current === "s") onChange(Math.min(Math.max(0, ms), endMs - 500), endMs);
        else onChange(startMs, Math.max(ms, startMs + 500));
      }}
      onPointerUp={() => (dragging.current = null)}
      onClick={(e) => {
        if (!dragging.current) onSeek(toMs(e.clientX));
      }}
    >
      {visible.map((w, i) => (
        <div
          key={i}
          className="absolute bottom-1 h-3 rounded-sm bg-white/15"
          style={{ left: `${x(w.s)}%`, width: `${Math.max(0.2, x(w.e) - x(w.s))}%` }}
          title={w.t}
        />
      ))}
      <div className="absolute top-0 h-full bg-accent/25" style={{ left: `${x(startMs)}%`, width: `${x(endMs) - x(startMs)}%` }} />
      {(["s", "e"] as const).map((k) => (
        <div
          key={k}
          className="absolute top-0 z-10 flex h-full w-3 -translate-x-1/2 cursor-ew-resize items-center justify-center rounded bg-accent"
          style={{ left: `${x(k === "s" ? startMs : endMs)}%` }}
          onPointerDown={(e) => {
            e.stopPropagation();
            (e.target as HTMLElement).setPointerCapture(e.pointerId);
            dragging.current = k;
          }}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="h-5 w-0.5 bg-white/80" />
        </div>
      ))}
      {t >= winS && t <= winE && <div className="pointer-events-none absolute top-0 h-full w-0.5 bg-white" style={{ left: `${x(t)}%` }} />}
      <span className="absolute left-1 top-0.5 text-[10px] text-muted">{fmtTime(winS)}</span>
      <span className="absolute right-1 top-0.5 text-[10px] text-muted">{fmtTime(winE)}</span>
    </div>
  );
}
