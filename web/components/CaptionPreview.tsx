"use client";

import type { Aspect, StyleConfig, Word } from "@/lib/api";

/** Mirrors captions.Group in the Go renderer so previews match output. */
export function groupWords(words: Word[], perLine: number, maxChars: number): Word[][] {
  const groups: Word[][] = [];
  let cur: Word[] = [];
  let chars = 0;
  words.forEach((w, i) => {
    const wl = [...w.t].length;
    if (cur.length > 0 && (cur.length >= perLine || chars + wl + 1 > maxChars || w.s - cur[cur.length - 1].e > 700)) {
      groups.push(cur);
      cur = [];
      chars = 0;
    }
    cur.push(w);
    chars += wl + 1;
    const t = w.t.trim();
    if ((t.endsWith(".") || t.endsWith("?") || t.endsWith("!")) && i + 1 < words.length) {
      groups.push(cur);
      cur = [];
      chars = 0;
    }
  });
  if (cur.length) groups.push(cur);
  return groups;
}

export function canvasSize(aspect: Aspect): [number, number] {
  if (aspect === "16:9") return [1920, 1080];
  if (aspect === "1:1") return [1080, 1080];
  return [1080, 1920];
}

/**
 * Renders the caption line active at time t (ms, same timebase as words)
 * inside a frame of pixel height `frameH` representing the output canvas.
 */
export function CaptionPreview({
  words,
  t,
  style,
  aspect,
  frameH,
}: {
  words: Word[];
  t: number;
  style: StyleConfig;
  aspect: Aspect;
  frameH: number;
}) {
  const [cw, ch] = canvasSize(aspect);
  const landscape = cw > ch;
  const groups = groupWords(words, Math.max(1, style.wordsPerLine), landscape ? 48 : 32);
  let gi = -1;
  for (let i = 0; i < groups.length; i++) {
    const g = groups[i];
    let end = g[g.length - 1].e;
    if (i + 1 < groups.length && groups[i + 1][0].s - end < 400) end = groups[i + 1][0].s;
    if (t >= g[0].s && t < end) {
      gi = i;
      break;
    }
  }
  if (gi < 0) return null;
  const g = groups[gi];
  const px = frameH / ch; // px per canvas unit
  const scale = landscape ? (ch / 1080) * 0.75 : ch / 1920;
  const fontSize = style.size * scale * px;
  const outline = style.outlineWidth * scale * px;
  const shadow = style.shadow * scale * px;
  const marginV = style.marginV * (ch / 1920) * px;
  const pos: React.CSSProperties =
    style.position === "middle"
      ? { top: "50%", transform: "translateY(-50%)" }
      : style.position === "top"
        ? { top: marginV }
        : { bottom: marginV };

  return (
    <div className="pointer-events-none absolute inset-x-0 flex justify-center px-[5.5%] text-center" style={pos}>
      <div
        style={{
          fontFamily: `"${style.font}", sans-serif`,
          fontWeight: style.weight,
          fontSize,
          lineHeight: 1.15,
          color: style.primary,
          WebkitTextStroke: `${outline}px ${style.outline}`,
          paintOrder: "stroke fill",
          textShadow: shadow > 0 ? `${shadow}px ${shadow}px 0 #000a` : undefined,
          textTransform: style.uppercase ? "uppercase" : "none",
        }}
      >
        {g.map((w, i) => {
          const active = t >= w.s && (i + 1 < g.length ? t < g[i + 1].s : true);
          let color = style.primary;
          let transform: string | undefined;
          if (style.animation === "karaoke") color = t >= w.s ? style.highlight : style.primary;
          else if ((style.animation === "highlight" || style.animation === "pop") && active) {
            color = style.highlight;
            if (style.animation === "pop") transform = "scale(1.1)";
          }
          return (
            <span key={i}>
              {i > 0 && " "}
              <span style={{ color, display: "inline-block", transform }}>{w.t}</span>
            </span>
          );
        })}
      </div>
    </div>
  );
}
