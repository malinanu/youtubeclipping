"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { FileVideo, Link2, Loader2, RotateCcw, Trash2, Upload as UploadIcon, X } from "lucide-react";
import { Shell, ProgressBar, StatusBadge } from "@/components/Shell";
import { api, type Video } from "@/lib/api";
import { usePoll, useServerEvents } from "@/lib/events";
import { cn, fmtTime, needsCookies } from "@/lib/util";
import { fmtBytes, Upload, type UploadProgress } from "@/lib/upload";

export default function Home() {
  const [videos, setVideos] = useState<Video[] | null>(null);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [mode, setMode] = useState<"url" | "upload">("url");

  const load = useCallback(() => api.videos().then(setVideos).catch((e) => setError(e.message)), []);
  useEffect(() => {
    load();
  }, [load]);

  const live = useServerEvents((e) => {
    if (e.kind !== "video") return;
    setVideos((vs) => {
      if (!vs) return vs;
      const found = vs.some((v) => v.id === e.id);
      if (!found || e.status === "ready" || e.stage === "metadata") {
        // Refresh metadata (title/thumbnail) at milestones.
        load();
        return vs;
      }
      return vs.map((v) =>
        v.id === e.id ? { ...v, status: e.status as Video["status"], stage: e.stage ?? v.stage, progress: e.progress, error: e.error ?? "" } : v,
      );
    });
  });

  // Fallback when live events don't arrive (e.g. a buffering proxy): poll while busy.
  const anyBusy = !!videos?.some((v) => v.status === "queued" || v.status === "downloading" || v.status === "transcribing" || v.status === "uploading");
  usePoll(anyBusy, live ? 15_000 : 3_000, load);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const v = await api.addVideo(url);
      setUrl("");
      setVideos((vs) => [v, ...(vs || [])]);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Shell>
      <div className="mb-2 flex gap-1">
        {(
          [
            ["url", "Paste URL", Link2],
            ["upload", "Upload file", UploadIcon],
          ] as const
        ).map(([k, label, Icon]) => (
          <button
            key={k}
            className={cn("flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm", mode === k ? "bg-panel2 text-white" : "text-muted hover:text-white")}
            onClick={() => setMode(k)}
          >
            <Icon size={14} /> {label}
          </button>
        ))}
      </div>
      {mode === "upload" ? (
        <UploadBox onStarted={load} />
      ) : (
      <form onSubmit={submit} className="card flex flex-col gap-3 p-5 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Link2 className="absolute left-3 top-1/2 -translate-y-1/2 text-muted" size={16} />
          <input
            className="input h-11 pl-9 text-base"
            placeholder="Paste a YouTube or Twitch URL (video, VOD or clip)"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            required
          />
        </div>
        <button className="btn-primary h-11 px-5" disabled={busy || !url.trim()}>
          {busy ? <Loader2 className="animate-spin" size={16} /> : null} Import
        </button>
      </form>
      )}
      {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

      <h2 className="mb-3 mt-8 text-sm font-semibold uppercase tracking-wide text-muted">Videos</h2>
      {videos === null ? (
        <p className="text-muted">Loading…</p>
      ) : videos.length === 0 ? (
        <p className="text-muted">No videos yet. Paste a URL or upload a file above to get started.</p>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {videos.map((v) => (
            <VideoCard key={v.id} v={v} onChange={load} />
          ))}
        </div>
      )}
    </Shell>
  );
}

type Active = { upload: Upload; file: File; p: UploadProgress | null; error: string };

function UploadBox({ onStarted }: { onStarted: () => void }) {
  const [active, setActive] = useState<Active[]>([]);
  const [drag, setDrag] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  const start = (files: FileList | null) => {
    for (const file of Array.from(files || [])) {
      const item: Active = { upload: null as unknown as Upload, file, p: null, error: "" };
      const up = new Upload(file, (p) => setActive((as) => as.map((a) => (a === item || a.file === file ? { ...a, p } : a))));
      item.upload = up;
      setActive((as) => [...as, item]);
      up.run()
        .then(() => {
          setActive((as) => as.filter((a) => a.file !== file));
          onStarted();
        })
        .catch((e) => setActive((as) => as.map((a) => (a.file === file ? { ...a, error: (e as Error).message } : a))));
      // Show the new "uploading" card as soon as the server created it.
      setTimeout(onStarted, 1500);
    }
  };

  return (
    <div className="card space-y-3 p-5">
      <div
        className={cn(
          "flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed px-4 py-8 text-center transition",
          drag ? "border-accent bg-accent/10" : "border-line hover:border-accent/60",
        )}
        onClick={() => input.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setDrag(true);
        }}
        onDragLeave={() => setDrag(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDrag(false);
          start(e.dataTransfer.files);
        }}
      >
        <FileVideo className="text-muted" />
        <div className="text-sm">
          <b>Drop a video here</b> or click to choose
        </div>
        <div className="text-xs text-muted">MP4, MOV, MKV, WEBM, M4V, AVI, TS, FLV · large files upload in chunks and resume if interrupted</div>
        <input
          ref={input}
          type="file"
          accept="video/*,.mkv,.ts,.flv,.m4v"
          multiple
          className="hidden"
          onChange={(e) => {
            start(e.target.files);
            e.target.value = "";
          }}
        />
      </div>
      {active.map((a) => {
        const pct = a.p ? a.p.sent / a.p.total : 0;
        return (
          <div key={a.file.name + a.file.size} className="space-y-1.5 rounded-md border border-line p-3">
            <div className="flex items-center gap-2 text-sm">
              <FileVideo size={14} className="text-muted" />
              <span className="truncate">{a.file.name}</span>
              <span className="ml-auto shrink-0 font-mono text-xs text-muted">
                {a.p ? `${fmtBytes(a.p.sent)} / ${fmtBytes(a.p.total)}` : fmtBytes(a.file.size)}
              </span>
              <button
                className="text-muted hover:text-red-300"
                title="Cancel upload"
                onClick={async () => {
                  await a.upload.cancel();
                  setActive((as) => as.filter((x) => x !== a));
                  onStarted();
                }}
              >
                <X size={14} />
              </button>
            </div>
            <ProgressBar value={pct} />
            <div className="text-xs text-muted">
              {a.error ? (
                <span className="text-red-400">
                  {a.error} — choose the same file again to resume.
                </span>
              ) : a.p ? (
                `${Math.round(pct * 100)}% · ${fmtBytes(a.p.bytesPerSec)}/s${a.p.etaSec > 0 ? ` · ${fmtTime(a.p.etaSec * 1000)} left` : ""}`
              ) : (
                "Starting…"
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function VideoCard({ v, onChange }: { v: Video; onChange: () => void }) {
  const busy = v.status === "queued" || v.status === "downloading" || v.status === "transcribing" || v.status === "uploading";
  return (
    <div className="card overflow-hidden">
      <Link href={`/videos/${v.id}`} className="block">
        <div className="relative aspect-video bg-panel2">
          {v.thumbnail && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={v.thumbnail} alt="" className="h-full w-full object-cover" referrerPolicy="no-referrer" />
          )}
          <span className="absolute bottom-2 right-2 rounded bg-black/75 px-1.5 py-0.5 text-xs">{fmtTime(v.durationMs)}</span>
          <span className="absolute left-2 top-2 rounded bg-black/75 px-1.5 py-0.5 text-[11px] uppercase">{v.platform}</span>
        </div>
      </Link>
      <div className="space-y-2 p-3">
        <Link href={`/videos/${v.id}`} className="line-clamp-2 font-medium hover:text-accent2">
          {v.title || v.sourceUrl}
        </Link>
        <div className="flex items-center gap-2 text-xs text-muted">
          <StatusBadge status={v.status} />
          {busy && v.stage && <span className="truncate">{v.stage}</span>}
          <span className="ml-auto">
            {v.meta?.upload ? `${fmtBytes(v.meta.upload.size)} · ` : ""}
            {v.clipCount} clips
          </span>
        </div>
        {busy && <ProgressBar value={v.progress} />}
        {v.status === "failed" && <p className="line-clamp-3 text-xs text-red-400" title={v.error}>{v.error}</p>}
        <div className="flex gap-2 pt-1">
          {v.status === "failed" && (
            <button className="btn-ghost" onClick={() => api.retryVideo(v.id).then(onChange)}>
              <RotateCcw size={14} /> Retry
            </button>
          )}
          {v.status === "failed" && needsCookies(v.error) && (
            <Link href="/settings#youtube" className="btn-ghost text-amber-300">
              Fix cookies
            </Link>
          )}
          <button
            className="btn-ghost ml-auto text-red-300"
            onClick={async () => {
              if (confirm("Delete this video, its clips and media?")) {
                await api.deleteVideo(v.id);
                onChange();
              }
            }}
          >
            <Trash2 size={14} />
          </button>
        </div>
      </div>
    </div>
  );
}
