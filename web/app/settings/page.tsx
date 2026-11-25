"use client";

import { useEffect, useState } from "react";
import { Save, Upload } from "lucide-react";
import { Shell } from "@/components/Shell";
import { api, type Settings, type SystemInfo } from "@/lib/api";

type SecretKey = keyof Settings["secrets"];

const WHISPER_MODELS = ["tiny", "base", "base.en", "small", "small.en", "medium", "medium.en", "large-v3-turbo"];

export default function SettingsPage() {
  const [s, setS] = useState<Settings | null>(null);
  const [sys, setSys] = useState<SystemInfo | null>(null);
  const [secrets, setSecrets] = useState<Record<SecretKey, string>>({ anthropicApiKey: "", openaiApiKey: "", groqApiKey: "", postizApiKey: "", geminiApiKey: "" });
  const [msg, setMsg] = useState("");

  useEffect(() => {
    api.settings().then(setS);
    api.system().then(setSys).catch(() => {});
  }, []);
  if (!s) return <Shell>Loading…</Shell>;
  const set = (p: Partial<Settings>) => setS({ ...s, ...p });

  async function save(clear: SecretKey[] = []) {
    if (!s) return;
    try {
      const { secrets: _masked, ...rest } = s;
      void _masked;
      const next = await api.saveSettings({ ...rest, ...secrets, clearSecrets: clear });
      setS(next);
      setSecrets({ anthropicApiKey: "", openaiApiKey: "", groqApiKey: "", postizApiKey: "", geminiApiKey: "" });
      setMsg("Saved");
      setTimeout(() => setMsg(""), 2000);
    } catch (e) {
      setMsg((e as Error).message);
    }
  }

  const secret = (k: SecretKey, label: string, hint?: string) => (
    <div>
      <label className="label">{label}</label>
      <div className="flex gap-2">
        <input
          className="input font-mono"
          type="password"
          autoComplete="off"
          placeholder={s.secrets[k] ? `${s.secrets[k]} (set)` : "not set"}
          value={secrets[k]}
          onChange={(e) => setSecrets({ ...secrets, [k]: e.target.value })}
        />
        {s.secrets[k] && (
          <button className="btn-ghost" onClick={() => save([k])}>
            Clear
          </button>
        )}
      </div>
      {hint && <p className="mt-1 text-xs text-muted">{hint}</p>}
    </div>
  );

  return (
    <Shell>
      <div className="mx-auto max-w-3xl space-y-6">
        <Section title="Transcription">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label">Provider</label>
              <select className="input" value={s.transcribeProvider} onChange={(e) => set({ transcribeProvider: e.target.value as Settings["transcribeProvider"] })}>
                <option value="local">Local whisper.cpp</option>
                <option value="openai">OpenAI Whisper API</option>
                <option value="groq">Groq Whisper API</option>
              </select>
            </div>
            <div>
              <label className="label">Local model</label>
              <select className="input" value={s.whisperModel} onChange={(e) => set({ whisperModel: e.target.value })}>
                {Array.from(new Set([s.whisperModel, ...WHISPER_MODELS, ...(sys?.whisperModels || [])])).map((m) => (
                  <option key={m} value={m}>
                    {m}
                    {sys?.whisperModels.includes(m) ? " ✓ downloaded" : ""}
                  </option>
                ))}
              </select>
              <p className="mt-1 text-xs text-muted">Models download automatically on first use (small ≈ 470 MB, medium ≈ 1.5 GB).</p>
            </div>
            <div>
              <label className="label">Language</label>
              <input className="input" value={s.whisperLanguage} onChange={(e) => set({ whisperLanguage: e.target.value })} placeholder="auto, en, si, ta…" />
            </div>
            <div>
              <label className="label">Fallback API</label>
              <select className="input" value={s.fallbackProvider} onChange={(e) => set({ fallbackProvider: e.target.value as Settings["fallbackProvider"] })}>
                <option value="none">None</option>
                <option value="openai">OpenAI</option>
                <option value="groq">Groq</option>
              </select>
            </div>
            <div className="col-span-2">
              <label className="label">Use fallback API first for videos longer than (minutes, 0 = only on failure)</label>
              <input className="input" type="number" value={s.fallbackOverMinutes} onChange={(e) => set({ fallbackOverMinutes: Number(e.target.value) })} />
            </div>
            {secret("openaiApiKey", "OpenAI API key")}
            {secret("groqApiKey", "Groq API key")}
          </div>
        </Section>

        <Section title="AI clip suggestions">
          <div className="grid grid-cols-2 gap-3">
            <div className="col-span-2">
              <label className="label">Provider</label>
              <select className="input" value={s.aiProvider} onChange={(e) => set({ aiProvider: e.target.value as Settings["aiProvider"] })}>
                <option value="auto">Auto (Claude if its key is set, otherwise Gemini)</option>
                <option value="claude">Claude (Anthropic)</option>
                <option value="gemini">Gemini (Google)</option>
              </select>
            </div>
            <div className="col-span-2">{secret("anthropicApiKey", "Anthropic API key", "Leave blank to keep the current key, or use ANTHROPIC_API_KEY in the environment.")}</div>
            <div className="col-span-2">
              <label className="label">Claude model</label>
              <input className="input font-mono" value={s.claudeModel} onChange={(e) => set({ claudeModel: e.target.value })} />
            </div>
            <div className="col-span-2">{secret("geminiApiKey", "Gemini API key", "Create one at aistudio.google.com → Get API key. Or set GEMINI_API_KEY in the environment.")}</div>
            <div className="col-span-2">
              <label className="label">Gemini model</label>
              <input className="input font-mono" value={s.geminiModel} onChange={(e) => set({ geminiModel: e.target.value })} placeholder="gemini-3.5-flash-lite" />
            </div>
            <div>
              <label className="label">Number of clips</label>
              <input className="input" type="number" value={s.suggestCount} onChange={(e) => set({ suggestCount: Number(e.target.value) })} />
            </div>
            <div>
              <label className="label">Min length (s)</label>
              <input className="input" type="number" value={s.suggestMinSec} onChange={(e) => set({ suggestMinSec: Number(e.target.value) })} />
            </div>
            <div>
              <label className="label">Max length (s)</label>
              <input className="input" type="number" value={s.suggestMaxSec} onChange={(e) => set({ suggestMaxSec: Number(e.target.value) })} />
            </div>
            <div className="col-span-2">
              <label className="label">Standing instructions</label>
              <textarea
                className="input min-h-20"
                value={s.suggestInstructions}
                onChange={(e) => set({ suggestInstructions: e.target.value })}
                placeholder="e.g. Audience is Sri Lankan tech founders; prefer practical tips and strong opinions."
              />
            </div>
          </div>
        </Section>

        <Section title="Postiz">
          <div className="space-y-3">
            <div>
              <label className="label">Public API URL</label>
              <input className="input font-mono" value={s.postizUrl} onChange={(e) => set({ postizUrl: e.target.value })} placeholder="https://postiz.example.com/api/public/v1" />
            </div>
            {secret("postizApiKey", "API key", "Postiz → Settings → Public API.")}
          </div>
        </Section>

        <div className="flex items-center gap-3">
          <button className="btn-primary" onClick={() => save()}>
            <Save size={14} /> Save settings
          </button>
          <span className="text-sm text-muted">{msg}</span>
        </div>

        <Section title="YouTube access (cookies)">
          <p className="mb-3 text-sm text-muted">
            Cloud IPs are often challenged by YouTube (&quot;Sign in to confirm you&apos;re not a bot&quot;). Export cookies.txt (Netscape format) from a logged-in browser
            session with a throwaway account and upload it here. Status: <b>{sys?.cookiesSet ? "uploaded" : "not set"}</b> · yt-dlp {sys?.ytdlpVersion}
          </p>
          <div className="flex gap-2">
            <label className="btn-ghost cursor-pointer">
              <Upload size={14} /> Upload cookies.txt
              <input
                type="file"
                accept=".txt"
                className="hidden"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  if (!f) return;
                  try {
                    await api.uploadCookies(f);
                    setSys(await api.system());
                    setMsg("Cookies uploaded");
                  } catch (err) {
                    setMsg((err as Error).message);
                  }
                }}
              />
            </label>
            {sys?.cookiesSet && (
              <button className="btn-danger" onClick={async () => (await api.deleteCookies(), setSys(await api.system()))}>
                Remove
              </button>
            )}
          </div>
        </Section>
      </div>
    </Shell>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="card p-5">
      <h2 className="mb-4 font-semibold">{title}</h2>
      {children}
    </section>
  );
}
