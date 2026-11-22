"use client";

import { useEffect, useState } from "react";
import { Plus, Save, Star, Trash2 } from "lucide-react";
import { Shell } from "@/components/Shell";
import { CaptionPreview } from "@/components/CaptionPreview";
import { api, type CaptionStyle, type StyleConfig, type Word } from "@/lib/api";
import { cn } from "@/lib/util";

const FONTS = ["Montserrat", "Inter", "Bebas Neue", "League Spartan", "Lato", "Roboto"];

const SAMPLE: Word[] = "This is how your captions will look on every clip you render".split(" ").map((t, i) => ({ s: i * 380, e: i * 380 + 340, t }));

const BLANK: StyleConfig = {
  font: "Montserrat",
  weight: 900,
  size: 78,
  primary: "#FFFFFF",
  highlight: "#FFE81F",
  outline: "#000000",
  outlineWidth: 6,
  shadow: 2,
  position: "bottom",
  marginV: 420,
  wordsPerLine: 3,
  uppercase: true,
  animation: "highlight",
};

export default function StylesPage() {
  const [styles, setStyles] = useState<CaptionStyle[]>([]);
  const [cur, setCur] = useState<CaptionStyle | null>(null);
  const [t, setT] = useState(0);
  const [msg, setMsg] = useState("");

  useEffect(() => {
    api.styles().then((s) => {
      setStyles(s);
      setCur(s[0] || null);
    });
  }, []);
  // Loop the sample sentence.
  useEffect(() => {
    const iv = setInterval(() => setT((x) => (x + 60) % (SAMPLE.length * 380 + 800)), 60);
    return () => clearInterval(iv);
  }, []);

  const set = (patch: Partial<StyleConfig>) => cur && setCur({ ...cur, config: { ...cur.config, ...patch } });

  async function save() {
    if (!cur) return;
    try {
      const s = await api.saveStyle(cur);
      const list = await api.styles();
      setStyles(list);
      setCur(list.find((x) => x.id === s.id) || s);
      setMsg("Saved");
      setTimeout(() => setMsg(""), 1500);
    } catch (e) {
      setMsg((e as Error).message);
    }
  }

  return (
    <Shell>
      <div className="grid gap-6 md:grid-cols-[220px_1fr_300px]">
        <div className="space-y-1">
          {styles.map((s) => (
            <button
              key={s.id}
              className={cn("flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm", cur?.id === s.id ? "bg-panel2" : "hover:bg-panel")}
              onClick={() => setCur(s)}
            >
              {s.isDefault && <Star size={12} className="text-amber-300" />} {s.name}
            </button>
          ))}
          <button className="btn-ghost mt-2 w-full justify-center" onClick={() => setCur({ id: "", name: "New style", config: BLANK, isDefault: false })}>
            <Plus size={14} /> New style
          </button>
        </div>

        {cur && (
          <div className="card space-y-4 p-4">
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2">
                <label className="label">Name</label>
                <input className="input" value={cur.name} onChange={(e) => setCur({ ...cur, name: e.target.value })} />
              </div>
              <div>
                <label className="label">Font</label>
                <select className="input" value={cur.config.font} onChange={(e) => set({ font: e.target.value })}>
                  {FONTS.map((f) => (
                    <option key={f}>{f}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="label">Weight</label>
                <select className="input" value={cur.config.weight} onChange={(e) => set({ weight: Number(e.target.value) })}>
                  {[400, 500, 600, 700, 800, 900].map((w) => (
                    <option key={w}>{w}</option>
                  ))}
                </select>
              </div>
              <Num label="Size (px @1080×1920)" v={cur.config.size} on={(size) => set({ size })} />
              <Num label="Words per line" v={cur.config.wordsPerLine} on={(wordsPerLine) => set({ wordsPerLine })} />
              <Color label="Text" v={cur.config.primary} on={(primary) => set({ primary })} />
              <Color label="Highlight" v={cur.config.highlight} on={(highlight) => set({ highlight })} />
              <Color label="Outline" v={cur.config.outline} on={(outline) => set({ outline })} />
              <Num label="Outline width" v={cur.config.outlineWidth} on={(outlineWidth) => set({ outlineWidth })} />
              <Num label="Shadow" v={cur.config.shadow} on={(shadow) => set({ shadow })} />
              <div>
                <label className="label">Animation</label>
                <select className="input" value={cur.config.animation} onChange={(e) => set({ animation: e.target.value as StyleConfig["animation"] })}>
                  <option value="highlight">Active word highlight</option>
                  <option value="pop">Highlight + pop</option>
                  <option value="karaoke">Karaoke fill</option>
                  <option value="none">Static</option>
                </select>
              </div>
              <div>
                <label className="label">Position</label>
                <select className="input" value={cur.config.position} onChange={(e) => set({ position: e.target.value as StyleConfig["position"] })}>
                  <option value="bottom">Bottom</option>
                  <option value="middle">Middle</option>
                  <option value="top">Top</option>
                </select>
              </div>
              <Num label="Vertical margin (px)" v={cur.config.marginV} on={(marginV) => set({ marginV })} />
              <label className="col-span-2 flex items-center gap-2 text-sm">
                <input type="checkbox" checked={cur.config.uppercase} onChange={(e) => set({ uppercase: e.target.checked })} /> UPPERCASE
              </label>
              <label className="col-span-2 flex items-center gap-2 text-sm">
                <input type="checkbox" checked={cur.isDefault} onChange={(e) => setCur({ ...cur, isDefault: e.target.checked })} /> Default for new clips
              </label>
            </div>
            <div className="flex items-center gap-2">
              <button className="btn-primary" onClick={save}>
                <Save size={14} /> Save
              </button>
              {cur.id && (
                <button
                  className="btn-danger"
                  onClick={async () => {
                    if (!confirm("Delete style?")) return;
                    await api.deleteStyle(cur.id);
                    const list = await api.styles();
                    setStyles(list);
                    setCur(list[0] || null);
                  }}
                >
                  <Trash2 size={14} />
                </button>
              )}
              <span className="text-sm text-muted">{msg}</span>
            </div>
          </div>
        )}

        {cur && (
          <div>
            <div className="relative mx-auto aspect-[9/16] w-full max-w-[300px] overflow-hidden rounded-xl bg-gradient-to-b from-slate-600 via-slate-800 to-slate-900">
              <CaptionPreview words={SAMPLE} t={t} style={cur.config} aspect="9:16" frameH={533} />
            </div>
            <p className="mt-2 text-center text-xs text-muted">Live preview (browser approximation of the libass render)</p>
          </div>
        )}
      </div>
    </Shell>
  );
}

function Num({ label, v, on }: { label: string; v: number; on: (n: number) => void }) {
  return (
    <div>
      <label className="label">{label}</label>
      <input className="input" type="number" value={v} onChange={(e) => on(Number(e.target.value))} />
    </div>
  );
}

function Color({ label, v, on }: { label: string; v: string; on: (s: string) => void }) {
  return (
    <div>
      <label className="label">{label}</label>
      <div className="flex gap-2">
        <input type="color" value={v} onChange={(e) => on(e.target.value.toUpperCase())} className="h-8 w-10 rounded border border-line bg-bg" />
        <input className="input font-mono" value={v} onChange={(e) => on(e.target.value)} />
      </div>
    </div>
  );
}
