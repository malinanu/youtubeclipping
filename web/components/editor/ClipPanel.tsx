"use client";

import { useEffect, useState } from "react";
import { Download, Film, Loader2, Play, Save, Send, Sparkles, Trash2, Type } from "lucide-react";
import { api, type Aspect, type CaptionStyle, type Clip, type Integration, type Publication, type Word } from "@/lib/api";
import { ProgressBar, StatusBadge } from "@/components/Shell";
import { cn, fileBase, fmtTime, parseTime } from "@/lib/util";

function TimeInput({ label, ms, onChange, onFromPlayhead }: { label: string; ms: number; onChange: (ms: number) => void; onFromPlayhead: () => void }) {
  const [text, setText] = useState(fmtTime(ms, true));
  useEffect(() => setText(fmtTime(ms, true)), [ms]);
  return (
    <div>
      <label className="label">{label}</label>
      <div className="flex gap-1">
        <button className="btn-ghost px-2" onClick={() => onChange(Math.max(0, ms - 500))} title="-0.5s">
          −
        </button>
        <input
          className="input text-center font-mono"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onBlur={() => {
            const v = parseTime(text);
            if (Number.isFinite(v)) onChange(v);
            else setText(fmtTime(ms, true));
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
        />
        <button className="btn-ghost px-2" onClick={() => onChange(ms + 500)} title="+0.5s">
          +
        </button>
        <button className="btn-ghost px-2 text-xs" onClick={onFromPlayhead} title="Set from playhead">
          ⌖
        </button>
      </div>
    </div>
  );
}

export function ClipPanel({
  draft,
  saved,
  styles,
  transcriptWords,
  playhead,
  onDraft,
  onSaved,
  onDeleted,
  onPlay,
}: {
  draft: Clip;
  saved: Clip;
  styles: CaptionStyle[];
  transcriptWords: Word[];
  playhead: () => number;
  onDraft: (c: Clip) => void;
  onSaved: (c: Clip) => void;
  onDeleted: (id: string) => void;
  onPlay: (s: number, e: number) => void;
}) {
  const [busy, setBusy] = useState<"" | "save" | "render">("");
  const [error, setError] = useState("");
  const [editWords, setEditWords] = useState(false);
  const [videoErr, setVideoErr] = useState<string | null>(null);
  const dirty = JSON.stringify(stripState(draft)) !== JSON.stringify(stripState(saved));
  const set = (patch: Partial<Clip>) => onDraft({ ...draft, ...patch });
  const rendering = saved.status === "queued" || saved.status === "rendering";

  const wordsInClip = mergeOverrides(transcriptWords, draft.wordOverrides).filter((w) => w.e > draft.startMs && w.s < draft.endMs);

  async function save(): Promise<Clip | null> {
    setBusy("save");
    setError("");
    try {
      const c = await api.updateClip(draft.id, stripState(draft));
      onSaved(c);
      return c;
    } catch (e) {
      setError((e as Error).message);
      return null;
    } finally {
      setBusy("");
    }
  }

  async function render() {
    if (dirty && !(await save())) return;
    setBusy("render");
    setError("");
    try {
      await api.renderClip(draft.id);
      onSaved({ ...saved, ...stripState(draft), status: "queued", progress: 0, error: "" });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy("");
    }
  }

  const dur = (draft.endMs - draft.startMs) / 1000;
  const version = saved.renderedAt || "";

  return (
    <div className="space-y-4 p-3">
      <div className="flex items-center gap-2">
        <StatusBadge status={saved.status} />
        {saved.source === "ai" && (
          <span className="flex items-center gap-1 rounded bg-accent/15 px-1.5 py-0.5 text-[11px] font-semibold text-violet-300" title={saved.aiReason}>
            <Sparkles size={11} /> AI {Math.round(saved.aiScore)}
          </span>
        )}
        <span className="ml-auto font-mono text-xs text-muted">{dur.toFixed(1)}s</span>
      </div>
      {saved.aiReason && <p className="rounded-md bg-panel2 p-2 text-xs text-muted">{saved.aiReason}</p>}

      <div>
        <label className="label">Title</label>
        <input className="input" value={draft.title} onChange={(e) => set({ title: e.target.value })} placeholder="Clip title" />
      </div>
      <div>
        <label className="label">Description / hook</label>
        <textarea className="input min-h-16" value={draft.description} onChange={(e) => set({ description: e.target.value })} />
      </div>

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        <TimeInput label="Start" ms={draft.startMs} onChange={(v) => set({ startMs: Math.min(v, draft.endMs - 500) })} onFromPlayhead={() => set({ startMs: Math.round(playhead()) })} />
        <TimeInput label="End" ms={draft.endMs} onChange={(v) => set({ endMs: Math.max(v, draft.startMs + 500) })} onFromPlayhead={() => set({ endMs: Math.round(playhead()) })} />
      </div>
      <button className="btn-ghost w-full justify-center" onClick={() => onPlay(draft.startMs, draft.endMs)}>
        <Play size={14} /> Preview clip
      </button>

      <div>
        <label className="label">Format</label>
        <div className="grid grid-cols-3 gap-1">
          {(["9:16", "1:1", "16:9"] as Aspect[]).map((a) => (
            <button key={a} className={cn("btn justify-center border", draft.aspect === a ? "border-accent bg-accent/20" : "border-line bg-panel2")} onClick={() => set({ aspect: a })}>
              {a}
            </button>
          ))}
        </div>
      </div>
      {draft.aspect !== "16:9" && (
        <div>
          <label className="label">Horizontal framing ({Math.round(draft.cropX * 100)}%)</label>
          <input type="range" min={0} max={1} step={0.005} value={draft.cropX} onChange={(e) => set({ cropX: Number(e.target.value) })} className="w-full accent-[#7c5cff]" />
          <p className="text-[11px] text-muted">Or drag the frame on the video.</p>
        </div>
      )}

      <div className="space-y-2">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={draft.captionsEnabled} onChange={(e) => set({ captionsEnabled: e.target.checked })} /> Burn in captions
        </label>
        {draft.captionsEnabled && (
          <>
            <select className="input" value={draft.captionStyleId || ""} onChange={(e) => set({ captionStyleId: e.target.value || null })}>
              {styles.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                  {s.isDefault ? " (default)" : ""}
                </option>
              ))}
            </select>
            <button className="btn-ghost w-full justify-center" onClick={() => setEditWords((v) => !v)}>
              <Type size={14} /> {editWords ? "Hide" : "Fix"} caption text ({wordsInClip.length} words)
            </button>
            {editWords && (
              <WordEditor
                words={wordsInClip}
                overridden={draft.wordOverrides.length > 0}
                onChange={(ws) => set({ wordOverrides: ws })}
                onReset={() => set({ wordOverrides: [] })}
              />
            )}
          </>
        )}
      </div>

      {error && <p className="text-sm text-red-400">{error}</p>}
      {saved.status === "failed" && saved.error && <p className="whitespace-pre-wrap rounded bg-red-950/40 p-2 text-xs text-red-300">{saved.error}</p>}

      <div className="flex gap-2">
        <button className="btn-ghost flex-1 justify-center" disabled={!dirty || !!busy} onClick={save}>
          {busy === "save" ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />} Save
        </button>
        <button className="btn-primary flex-1 justify-center" disabled={rendering || !!busy} onClick={render}>
          {busy === "render" || rendering ? <Loader2 size={14} className="animate-spin" /> : <Film size={14} />}
          {saved.hasOutput ? "Re-render" : "Render"}
        </button>
      </div>
      {rendering && <ProgressBar value={saved.progress} />}

      {saved.hasOutput && !rendering && (
        <div className="space-y-2 rounded-lg border border-line p-2">
          <video
            key={version}
            src={api.clipFile(saved.id, "mp4", false, version)}
            poster={api.clipFile(saved.id, "thumb", false, version)}
            onError={() => setVideoErr(version)}
            onLoadedData={() => setVideoErr(null)}
            controls
            className={cn("mx-auto max-h-[420px] rounded bg-black", saved.aspect === "16:9" ? "w-full" : "h-[420px]")}
          />
          <div className="flex gap-2">
            <a className="btn-ghost flex-1 justify-center" href={api.clipFile(saved.id, "mp4", true, version)} download={`${fileBase(saved.title)}.mp4`}>
              <Download size={14} /> MP4
            </a>
            <a className="btn-ghost flex-1 justify-center" href={api.clipFile(saved.id, "srt", true, version)} download={`${fileBase(saved.title)}.srt`}>
              <Download size={14} /> SRT
            </a>
          </div>
          {videoErr === version && <p className="text-[11px] text-red-400">Couldn&apos;t load the rendered file. Re-render the clip.</p>}
          {dirty && <p className="text-[11px] text-amber-300">Unsaved changes are not in this render.</p>}
        </div>
      )}

      {saved.hasOutput && <PublishBox clip={saved} />}

      <button
        className="btn-danger w-full justify-center"
        onClick={async () => {
          if (!confirm("Delete this clip?")) return;
          await api.deleteClip(saved.id);
          onDeleted(saved.id);
        }}
      >
        <Trash2 size={14} /> Delete clip
      </button>
    </div>
  );
}

/** Overrides replace transcript words inside their own time span; transcript fills the rest. Mirrors media.MergeOverrides. */
export function mergeOverrides(words: Word[], overrides: Word[]): Word[] {
  if (!overrides.length) return words;
  const a = overrides[0].s;
  const b = overrides[overrides.length - 1].e;
  return [...words.filter((w) => w.e <= a), ...overrides, ...words.filter((w) => w.s >= b)];
}

function stripState(c: Clip) {
  return {
    startMs: c.startMs,
    endMs: c.endMs,
    title: c.title,
    description: c.description,
    aspect: c.aspect,
    cropX: c.cropX,
    captionsEnabled: c.captionsEnabled,
    captionStyleId: c.captionStyleId,
    wordOverrides: c.wordOverrides,
  };
}

function WordEditor({ words, overridden, onChange, onReset }: { words: Word[]; overridden: boolean; onChange: (w: Word[]) => void; onReset: () => void }) {
  return (
    <div className="space-y-2 rounded-md border border-line p-2">
      <div className="flex max-h-56 flex-wrap gap-1 overflow-y-auto">
        {words.map((w, i) => (
          <input
            key={`${w.s}-${i}`}
            className="rounded border border-line bg-bg px-1 py-0.5 text-sm outline-none focus:border-accent"
            style={{ width: `${Math.max(3, w.t.length + 1)}ch` }}
            value={w.t}
            title={fmtTime(w.s, true)}
            onChange={(e) => {
              onChange(words.map((x, j) => (j === i ? { ...x, t: e.target.value } : x)));
            }}
          />
        ))}
      </div>
      <div className="flex items-center justify-between text-[11px] text-muted">
        <span>Edits apply to this clip only. Clear a word to remove it.</span>
        {overridden && (
          <button className="text-accent2 hover:underline" onClick={onReset}>
            Reset to transcript
          </button>
        )}
      </div>
    </div>
  );
}

function PublishBox({ clip }: { clip: Clip }) {
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<{ configured: boolean; integrations: Integration[] } | null>(null);
  const [picked, setPicked] = useState<Record<string, boolean>>({});
  const [content, setContent] = useState(`${clip.title}\n\n${clip.description}`.trim());
  const [when, setWhen] = useState("");
  const [draft, setDraft] = useState(false);
  const [pubs, setPubs] = useState<Publication[]>([]);
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);

  useEffect(() => {
    if (!open) return;
    api.integrations().then(setData).catch((e) => setError(e.message));
    api.publications(clip.id).then(setPubs).catch(() => {});
    const iv = setInterval(() => api.publications(clip.id).then(setPubs).catch(() => {}), 5000);
    return () => clearInterval(iv);
  }, [open, clip.id]);

  if (!open)
    return (
      <button className="btn-ghost w-full justify-center" onClick={() => setOpen(true)}>
        <Send size={14} /> Publish via Postiz
      </button>
    );

  return (
    <div className="space-y-3 rounded-lg border border-line p-3">
      <div className="text-sm font-semibold">Publish via Postiz</div>
      {!data ? (
        <p className="text-sm text-muted">Loading channels…</p>
      ) : !data.configured ? (
        <p className="text-sm text-muted">
          Postiz is not configured. Add the API URL and key in <a className="text-accent2 underline" href="/settings">Settings</a>.
        </p>
      ) : (
        <>
          <div className="space-y-1">
            {data.integrations.filter((i) => !i.disabled).map((i) => (
              <label key={i.id} className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={!!picked[i.id]} onChange={(e) => setPicked({ ...picked, [i.id]: e.target.checked })} />
                {/* eslint-disable-next-line @next/next/no-img-element */}
                {i.picture && <img src={i.picture} alt="" className="h-5 w-5 rounded-full" />}
                {i.name} <span className="text-xs text-muted">{i.identifier}</span>
              </label>
            ))}
            {data.integrations.length === 0 && <p className="text-sm text-muted">No channels connected in Postiz yet.</p>}
          </div>
          <textarea className="input min-h-24" value={content} onChange={(e) => setContent(e.target.value)} placeholder="Post text, hashtags…" />
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="label">Schedule (empty = now)</label>
              <input className="input" type="datetime-local" value={when} onChange={(e) => setWhen(e.target.value)} />
            </div>
            <label className="mt-5 flex items-center gap-2 text-sm">
              <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} /> Save as draft
            </label>
          </div>
          {error && <p className="text-sm text-red-400">{error}</p>}
          <button
            className="btn-primary w-full justify-center"
            disabled={sending || !Object.values(picked).some(Boolean)}
            onClick={async () => {
              setSending(true);
              setError("");
              try {
                const targets = data.integrations.filter((i) => picked[i.id]).map((i) => ({ integrationId: i.id, identifier: i.identifier }));
                const p = await api.publish(clip.id, { targets, content, scheduledAt: when ? new Date(when).toISOString() : null, draft });
                setPubs((ps) => [p, ...ps]);
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setSending(false);
              }
            }}
          >
            {sending ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />} Send to Postiz
          </button>
        </>
      )}
      {pubs.length > 0 && (
        <div className="space-y-1 border-t border-line pt-2">
          {pubs.map((p) => (
            <div key={p.id} className="flex items-center gap-2 text-xs">
              <StatusBadge status={p.status} />
              <span className="text-muted">{new Date(p.createdAt).toLocaleString()}</span>
              {p.scheduledAt && <span className="text-muted">→ {new Date(p.scheduledAt).toLocaleString()}</span>}
              {p.error && <span className="truncate text-red-400" title={p.error}>{p.error}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
