"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Link2, Loader2, RotateCcw, Trash2 } from "lucide-react";
import { Shell, ProgressBar, StatusBadge } from "@/components/Shell";
import { api, type Video } from "@/lib/api";
import { useServerEvents } from "@/lib/events";
import { fmtTime, needsCookies } from "@/lib/util";

export default function Home() {
  const [videos, setVideos] = useState<Video[] | null>(null);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(() => api.videos().then(setVideos).catch((e) => setError(e.message)), []);
  useEffect(() => {
    load();
  }, [load]);

  useServerEvents((e) => {
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
      {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

      <h2 className="mb-3 mt-8 text-sm font-semibold uppercase tracking-wide text-muted">Videos</h2>
      {videos === null ? (
        <p className="text-muted">Loading…</p>
      ) : videos.length === 0 ? (
        <p className="text-muted">No videos yet. Paste a URL above to get started.</p>
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

function VideoCard({ v, onChange }: { v: Video; onChange: () => void }) {
  const busy = v.status === "queued" || v.status === "downloading" || v.status === "transcribing";
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
          <span className="ml-auto">{v.clipCount} clips</span>
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
