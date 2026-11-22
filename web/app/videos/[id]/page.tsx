"use client";

import { use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ExternalLink, Loader2, Plus, RotateCcw, Sparkles } from "lucide-react";
import { Shell, ProgressBar, StatusBadge } from "@/components/Shell";
import { Player, type PlayerHandle } from "@/components/editor/Player";
import { Timeline, Trimmer } from "@/components/editor/Timeline";
import { TranscriptPanel } from "@/components/editor/TranscriptPanel";
import { ClipPanel, mergeOverrides } from "@/components/editor/ClipPanel";
import { api, type CaptionStyle, type Clip, type Transcript, type Video } from "@/lib/api";
import { useServerEvents } from "@/lib/events";
import { cn, fmtTime } from "@/lib/util";

export default function VideoPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [video, setVideo] = useState<Video | null>(null);
  const [transcript, setTranscript] = useState<Transcript | null>(null);
  const [clips, setClips] = useState<Clip[]>([]);
  const [styles, setStyles] = useState<CaptionStyle[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [draft, setDraft] = useState<Clip | null>(null);
  const [t, setT] = useState(0);
  const [tab, setTab] = useState<"transcript" | "clips">("transcript");
  const [markIn, setMarkIn] = useState<number | null>(null);
  const [markOut, setMarkOut] = useState<number | null>(null);
  const [suggest, setSuggest] = useState<{ status: string; error?: string; count?: number }>({ status: "idle" });
  const [suggestOpts, setSuggestOpts] = useState({ open: false, instructions: "", replace: true });
  const [error, setError] = useState("");
  const player = useRef<PlayerHandle>(null);

  const loadClips = useCallback(() => api.clips(id).then(setClips), [id]);
  useEffect(() => {
    api.video(id).then(setVideo).catch((e) => setError(e.message));
    api.transcript(id).then(setTranscript).catch(() => {});
    api.styles().then(setStyles).catch(() => {});
    api.suggestStatus(id).then(setSuggest).catch(() => {});
    loadClips();
  }, [id, loadClips]);

  useServerEvents((e) => {
    if (e.videoId !== id && e.id !== id) return;
    if (e.kind === "video") {
      setVideo((v) => (v ? { ...v, status: e.status as Video["status"], stage: e.stage ?? "", progress: e.progress, error: e.error ?? "" } : v));
      if (e.status === "ready") {
        api.video(id).then(setVideo);
        api.transcript(id).then(setTranscript).catch(() => {});
      }
      if (e.stage === "proxy" && e.progress === 0) api.video(id).then(setVideo);
    } else if (e.kind === "clip") {
      setClips((cs) => cs.map((c) => (c.id === e.id ? { ...c, status: e.status as Clip["status"], progress: e.progress, error: e.error ?? "" } : c)));
      if (e.status === "done" || e.status === "failed") api.clip(e.id).then((c) => setClips((cs) => cs.map((x) => (x.id === c.id ? c : x))));
    } else if (e.kind === "suggest") {
      setSuggest({ status: e.status, error: e.error });
      if (e.status === "done") {
        loadClips();
        setTab("clips");
      }
    }
  });

  const saved = clips.find((c) => c.id === selectedId) || null;
  // Keep the draft in sync when another clip is selected or the server version changes.
  useEffect(() => {
    if (!saved) {
      setDraft(null);
      return;
    }
    setDraft((d) => (d && d.id === saved.id && d.updatedAt === saved.updatedAt ? d : saved));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved?.id, saved?.updatedAt]);

  const styleFor = useCallback(
    (c: Clip) => (styles.find((s) => s.id === c.captionStyleId) || styles.find((s) => s.isDefault) || styles[0])?.config,
    [styles],
  );

  const captionWords = useMemo(() => {
    if (!draft || !transcript) return [];
    return mergeOverrides(transcript.words, draft.wordOverrides).filter((w) => w.t.trim() !== "" && w.e > draft.startMs && w.s < draft.endMs);
  }, [draft, transcript]);

  const onTime = useCallback((ms: number) => setT(ms), []);
  const seek = useCallback((ms: number) => player.current?.seek(ms), []);

  const selectClip = (cid: string) => {
    setSelectedId(cid);
    setTab("clips");
    const c = clips.find((x) => x.id === cid);
    if (c) seek(c.startMs);
  };

  async function createClip(s: number, e: number, title = "") {
    try {
      const c = await api.createClip(id, { startMs: Math.max(0, Math.round(s)), endMs: Math.round(e), title, aspect: "9:16", cropX: 0.5, captionsEnabled: true });
      setClips((cs) => [...cs, c].sort((a, b) => a.startMs - b.startMs));
      setSelectedId(c.id);
      setTab("clips");
      setMarkIn(null);
      setMarkOut(null);
    } catch (err) {
      setError((err as Error).message);
    }
  }

  // Keyboard: space play/pause, I/O mark in/out, ←/→ seek, Enter create clip from marks.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (e.target as HTMLElement).isContentEditable) return;
      const now = player.current?.time() ?? 0;
      if (e.key === " ") {
        e.preventDefault();
        player.current?.toggle();
      } else if (e.key === "i" || e.key === "I") setMarkIn(Math.round(now));
      else if (e.key === "o" || e.key === "O") setMarkOut(Math.round(now));
      else if (e.key === "ArrowLeft") seek(now - (e.shiftKey ? 100 : 2000));
      else if (e.key === "ArrowRight") seek(now + (e.shiftKey ? 100 : 2000));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [seek]);

  if (error && !video) return <Shell>{error}</Shell>;
  if (!video) return <Shell>Loading…</Shell>;

  const processing = video.status !== "ready" && video.status !== "failed";
  const marks = markIn !== null && markOut !== null && markOut > markIn ? { s: markIn, e: markOut } : null;
  const activeStyle = draft ? styleFor(draft) : undefined;

  return (
    <Shell wide>
      <div className="mb-4 flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-xl font-bold">{video.title || video.sourceUrl}</h1>
          <div className="mt-1 flex items-center gap-2 text-sm text-muted">
            <StatusBadge status={video.status} />
            <span>{video.channel}</span>·<span>{fmtTime(video.durationMs)}</span>
            <a href={video.meta?.webpageUrl || video.sourceUrl} target="_blank" rel="noreferrer" className="flex items-center gap-1 hover:text-white">
              source <ExternalLink size={12} />
            </a>
          </div>
        </div>
        {video.status === "ready" && (
          <button className="btn-ghost" onClick={() => confirm("Re-run transcription? Existing clips are kept.") && api.retranscribe(id).then(() => api.video(id).then(setVideo))}>
            <RotateCcw size={14} /> Re-transcribe
          </button>
        )}
        {video.status === "failed" && (
          <button className="btn-ghost" onClick={() => api.retryVideo(id).then(() => api.video(id).then(setVideo))}>
            <RotateCcw size={14} /> Retry
          </button>
        )}
      </div>

      {processing && (
        <div className="card mb-4 space-y-2 p-4">
          <div className="text-sm">
            {video.status === "downloading" ? "Downloading" : video.status === "transcribing" ? "Transcribing" : "Queued"}
            {video.stage ? ` · ${video.stage}` : ""} — {Math.round(video.progress * 100)}%
          </div>
          <ProgressBar value={video.progress} />
          <p className="text-xs text-muted">You can start scrubbing as soon as the preview finishes downloading. Long VODs take a while to transcribe on CPU.</p>
        </div>
      )}
      {video.status === "failed" && <div className="card mb-4 whitespace-pre-wrap p-4 text-sm text-red-300">{video.error}</div>}
      {error && <p className="mb-3 text-sm text-red-400">{error}</p>}

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_440px]">
        <div className="min-w-0 space-y-3">
          {video.hasProxy ? (
            <Player
              ref={player}
              src={api.proxyUrl(id)}
              onTime={onTime}
              overlay={
                draft
                  ? {
                      aspect: draft.aspect,
                      cropX: draft.cropX,
                      onCropX: (x) => setDraft({ ...draft, cropX: x }),
                      captions: draft.captionsEnabled && activeStyle ? { words: captionWords, style: activeStyle } : null,
                      active: t >= draft.startMs && t <= draft.endMs,
                    }
                  : null
              }
            />
          ) : (
            <div className="grid aspect-video place-items-center rounded-lg bg-panel2 text-muted">
              <Loader2 className="animate-spin" />
            </div>
          )}

          {video.durationMs > 0 && (
            <Timeline
              durationMs={video.durationMs}
              t={t}
              clips={clips}
              selectedId={selectedId}
              selection={marks}
              chapters={video.meta?.chapters}
              onSeek={seek}
              onSelectClip={selectClip}
            />
          )}

          {draft && (
            <div className="card space-y-2 p-3">
              <div className="flex items-center justify-between text-xs text-muted">
                <span>Trim “{draft.title || "Untitled"}” — drag the handles</span>
                <span className="font-mono">
                  {fmtTime(draft.startMs, true)} → {fmtTime(draft.endMs, true)}
                </span>
              </div>
              <Trimmer
                startMs={draft.startMs}
                endMs={draft.endMs}
                durationMs={video.durationMs}
                t={t}
                words={transcript?.words || []}
                onChange={(s, e) => setDraft({ ...draft, startMs: s, endMs: e })}
                onSeek={seek}
              />
            </div>
          )}

          <div className="card flex flex-wrap items-center gap-2 p-3 text-sm">
            <span className="text-muted">Mark:</span>
            <button className="btn-ghost" onClick={() => setMarkIn(Math.round(player.current?.time() ?? 0))}>
              In [I] {markIn !== null && <span className="font-mono text-xs">{fmtTime(markIn, true)}</span>}
            </button>
            <button className="btn-ghost" onClick={() => setMarkOut(Math.round(player.current?.time() ?? 0))}>
              Out [O] {markOut !== null && <span className="font-mono text-xs">{fmtTime(markOut, true)}</span>}
            </button>
            <button className="btn-primary" disabled={!marks} onClick={() => marks && createClip(marks.s, marks.e)}>
              <Plus size={14} /> Create clip
            </button>
            <span className="ml-auto hidden text-xs text-muted xl:inline">Space play/pause · ←/→ 2s · Shift+←/→ 0.1s</span>
          </div>
        </div>

        <div className="card flex h-[calc(100vh-150px)] min-h-[520px] flex-col overflow-hidden lg:sticky lg:top-20">
          <div className="flex border-b border-line">
            {(["transcript", "clips"] as const).map((k) => (
              <button
                key={k}
                className={cn("flex-1 py-2.5 text-sm font-medium capitalize", tab === k ? "border-b-2 border-accent text-white" : "text-muted hover:text-white")}
                onClick={() => setTab(k)}
              >
                {k}
                {k === "clips" ? ` (${clips.length})` : ""}
              </button>
            ))}
          </div>

          {tab === "transcript" ? (
            transcript ? (
              <TranscriptPanel
                transcript={transcript}
                t={t}
                clips={clips}
                onSeek={seek}
                onCreateClip={(s, e, title) => createClip(s, e, title)}
                onPlayRange={(s, e) => player.current?.playRange(s, e)}
              />
            ) : (
              <div className="p-6 text-center text-sm text-muted">{processing ? "Transcript will appear when transcription finishes." : "No transcript."}</div>
            )
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="space-y-2 border-b border-line p-3">
                <button
                  className="btn-primary w-full justify-center"
                  disabled={!transcript || suggest.status === "running" || suggest.status === "queued"}
                  onClick={() => setSuggestOpts((o) => ({ ...o, open: !o.open }))}
                >
                  {suggest.status === "running" || suggest.status === "queued" ? <Loader2 size={14} className="animate-spin" /> : <Sparkles size={14} />}
                  {suggest.status === "running" || suggest.status === "queued" ? "Finding highlights…" : "Suggest clips with AI"}
                </button>
                {suggestOpts.open && suggest.status !== "running" && (
                  <div className="space-y-2">
                    <textarea
                      className="input min-h-16"
                      placeholder="Optional: what to look for (e.g. funny moments, key insights about pricing)"
                      value={suggestOpts.instructions}
                      onChange={(e) => setSuggestOpts({ ...suggestOpts, instructions: e.target.value })}
                    />
                    <label className="flex items-center gap-2 text-xs text-muted">
                      <input type="checkbox" checked={suggestOpts.replace} onChange={(e) => setSuggestOpts({ ...suggestOpts, replace: e.target.checked })} />
                      Replace previous un-rendered AI suggestions
                    </label>
                    <button
                      className="btn-ghost w-full justify-center"
                      onClick={async () => {
                        try {
                          await api.suggest(id, suggestOpts.replace, suggestOpts.instructions);
                          setSuggest({ status: "queued" });
                          setSuggestOpts({ ...suggestOpts, open: false });
                        } catch (e) {
                          setSuggest({ status: "failed", error: (e as Error).message });
                        }
                      }}
                    >
                      Run
                    </button>
                  </div>
                )}
                {suggest.status === "failed" && <p className="text-xs text-red-400">{suggest.error}</p>}
              </div>

              {clips.length === 0 && <p className="p-6 text-center text-sm text-muted">No clips yet. Select text in the transcript, mark In/Out, or ask AI.</p>}
              <div className="divide-y divide-line">
                {clips.map((c) => (
                  <div key={c.id}>
                    <button className={cn("flex w-full items-start gap-2 p-3 text-left hover:bg-panel2", c.id === selectedId && "bg-panel2")} onClick={() => selectClip(c.id)}>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">{c.title || "Untitled clip"}</div>
                        <div className="mt-0.5 flex items-center gap-2 font-mono text-[11px] text-muted">
                          {fmtTime(c.startMs)}–{fmtTime(c.endMs)} · {((c.endMs - c.startMs) / 1000).toFixed(0)}s · {c.aspect}
                        </div>
                        {(c.status === "rendering" || c.status === "queued") && <ProgressBar value={c.progress} className="mt-1.5" />}
                      </div>
                      <div className="flex flex-col items-end gap-1">
                        <StatusBadge status={c.status} />
                        {c.source === "ai" && <span className="text-[11px] text-violet-300">★ {Math.round(c.aiScore)}</span>}
                      </div>
                    </button>
                    {c.id === selectedId && draft && draft.id === c.id && (
                      <div className="border-t border-line bg-bg/40">
                        <ClipPanel
                          draft={draft}
                          saved={c}
                          styles={styles}
                          transcriptWords={transcript?.words || []}
                          playhead={() => player.current?.time() ?? 0}
                          onDraft={setDraft}
                          onSaved={(n) => {
                            setClips((cs) => cs.map((x) => (x.id === n.id ? n : x)).sort((a, b) => a.startMs - b.startMs));
                            setDraft(n);
                          }}
                          onDeleted={(cid) => {
                            setClips((cs) => cs.filter((x) => x.id !== cid));
                            setSelectedId(null);
                          }}
                          onPlay={(s, e) => player.current?.playRange(s, e)}
                        />
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </Shell>
  );
}
