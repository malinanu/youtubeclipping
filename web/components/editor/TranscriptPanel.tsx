"use client";

import { memo, useEffect, useMemo, useRef, useState } from "react";
import { Scissors, Play, Search } from "lucide-react";
import type { Clip, Transcript, Word } from "@/lib/api";
import { fmtTime, wordIndexAt } from "@/lib/util";

type Seg = { s: number; e: number; from: number; to: number }; // word index range [from,to)

function buildSegs(tr: Transcript): Seg[] {
  const out: Seg[] = [];
  let wi = 0;
  for (const seg of tr.segments) {
    const from = wi;
    while (wi < tr.words.length && tr.words[wi].s <= seg.e) wi++;
    if (wi > from) out.push({ s: seg.s, e: seg.e, from, to: wi });
  }
  if (wi < tr.words.length) out.push({ s: tr.words[wi].s, e: tr.words[tr.words.length - 1].e, from: wi, to: tr.words.length });
  return out;
}

const SegmentRow = memo(function SegmentRow({
  seg,
  words,
  selFrom,
  selTo,
  clipRanges,
}: {
  seg: Seg;
  words: Word[];
  selFrom: number;
  selTo: number;
  clipRanges: string; // serialized ranges to keep memo cheap
}) {
  const ranges = useMemo(() => (clipRanges ? clipRanges.split(";").map((r) => r.split(",").map(Number)) : []), [clipRanges]);
  const spans = [];
  for (let i = seg.from; i < seg.to; i++) {
    const w = words[i];
    const sel = i >= selFrom && i <= selTo;
    const inClip = ranges.some(([a, b]) => w.s >= a && w.e <= b);
    spans.push(
      <span key={i} data-i={i} className={`word${sel ? " sel" : ""}${inClip ? " inclip" : ""}`}>
        {w.t}
      </span>,
      " ",
    );
  }
  return (
    <div className="flex gap-3 py-1.5" data-seg={seg.from}>
      <span className="w-12 shrink-0 pt-0.5 text-right font-mono text-[11px] text-muted">{fmtTime(seg.s)}</span>
      <p className="leading-relaxed">{spans}</p>
    </div>
  );
});

export function TranscriptPanel({
  transcript,
  t,
  clips,
  onSeek,
  onCreateClip,
  onPlayRange,
}: {
  transcript: Transcript;
  t: number;
  clips: Clip[];
  onSeek: (ms: number) => void;
  onCreateClip: (s: number, e: number, title: string) => void;
  onPlayRange: (s: number, e: number) => void;
}) {
  const words = transcript.words;
  const segs = useMemo(() => buildSegs(transcript), [transcript]);
  const box = useRef<HTMLDivElement>(null);
  const [sel, setSel] = useState<[number, number] | null>(null);
  const anchor = useRef<number | null>(null);
  const mouseDown = useRef(false);
  const [follow, setFollow] = useState(true);
  const [query, setQuery] = useState("");

  const clipRanges = useMemo(() => clips.map((c) => `${c.startMs},${c.endMs}`).join(";"), [clips]);

  // Highlight the active word via DOM to avoid re-rendering thousands of spans.
  const activeIdx = useRef(-1);
  useEffect(() => {
    const idx = wordIndexAt(words, t);
    if (idx === activeIdx.current || !box.current) return;
    box.current.querySelector(`[data-i="${activeIdx.current}"]`)?.classList.remove("active");
    const el = box.current.querySelector(`[data-i="${idx}"]`) as HTMLElement | null;
    if (el && idx >= 0 && t <= words[idx].e + 600) {
      el.classList.add("active");
      if (follow) {
        const r = el.getBoundingClientRect();
        const br = box.current.getBoundingClientRect();
        if (r.top < br.top + 40 || r.bottom > br.bottom - 40) el.scrollIntoView({ block: "center", behavior: "smooth" });
      }
    }
    activeIdx.current = idx;
  }, [t, words, follow]);

  const wordFromEvent = (e: React.MouseEvent) => {
    const el = (e.target as HTMLElement).closest("[data-i]") as HTMLElement | null;
    return el ? Number(el.dataset.i) : null;
  };

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return segs;
    return segs.filter((s) =>
      words
        .slice(s.from, s.to)
        .map((w) => w.t)
        .join(" ")
        .toLowerCase()
        .includes(q),
    );
  }, [segs, words, query]);

  const selRange = sel ? { s: words[sel[0]].s, e: words[sel[1]].e } : null;
  const selText = sel ? words.slice(sel[0], sel[1] + 1).map((w) => w.t).join(" ") : "";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b border-line p-2">
        <div className="relative flex-1">
          <Search size={14} className="absolute left-2 top-1/2 -translate-y-1/2 text-muted" />
          <input className="input pl-7" placeholder="Search transcript" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        <label className="flex items-center gap-1 text-xs text-muted">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
        </label>
      </div>
      <div
        ref={box}
        className="min-h-0 flex-1 select-none overflow-y-auto px-3 py-2 text-[15px]"
        onMouseDown={(e) => {
          const i = wordFromEvent(e);
          if (i === null) return;
          mouseDown.current = true;
          if (e.shiftKey && anchor.current !== null) {
            setSel([Math.min(anchor.current, i), Math.max(anchor.current, i)]);
          } else {
            anchor.current = i;
            setSel(null);
          }
        }}
        onMouseOver={(e) => {
          if (!mouseDown.current || anchor.current === null) return;
          const i = wordFromEvent(e);
          if (i !== null && i !== anchor.current) setSel([Math.min(anchor.current, i), Math.max(anchor.current, i)]);
        }}
        onMouseUp={(e) => {
          mouseDown.current = false;
          const i = wordFromEvent(e);
          if (i !== null && i === anchor.current && !e.shiftKey) onSeek(words[i].s);
        }}
        onMouseLeave={() => (mouseDown.current = false)}
      >
        {shown.length === 0 && <p className="py-6 text-center text-sm text-muted">No matches.</p>}
        {shown.map((seg) => (
          <SegmentRow
            key={seg.from}
            seg={seg}
            words={words}
            selFrom={sel && sel[1] >= seg.from && sel[0] < seg.to ? sel[0] : -1}
            selTo={sel && sel[1] >= seg.from && sel[0] < seg.to ? sel[1] : -2}
            clipRanges={clipRanges}
          />
        ))}
      </div>
      {/* Selection actions live in the footer so the transcript never shifts mid-drag. */}
      {selRange ? (
        <div className="flex min-h-11 flex-wrap items-center gap-2 border-t border-line bg-panel2 px-2 py-1.5 text-xs">
          <span className="text-muted">
            {fmtTime(selRange.s, true)}–{fmtTime(selRange.e, true)} ({((selRange.e - selRange.s) / 1000).toFixed(1)}s)
          </span>
          <button className="btn-ghost ml-auto py-1" onClick={() => onPlayRange(selRange.s, selRange.e)}>
            <Play size={13} /> Play
          </button>
          <button
            className="btn-primary py-1"
            onClick={() => {
              onCreateClip(selRange.s, selRange.e + 250, selText.slice(0, 70));
              setSel(null);
            }}
          >
            <Scissors size={13} /> Create clip
          </button>
          <button className="text-muted hover:text-white" onClick={() => setSel(null)}>
            ✕
          </button>
        </div>
      ) : (
        <p className="flex min-h-11 items-center border-t border-line px-3 py-1.5 text-[11px] text-muted">
          Click a word to seek · drag or shift-click to select · {words.length.toLocaleString()} words · {transcript.provider}
        </p>
      )}
    </div>
  );
}
