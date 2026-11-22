"use client";

import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { Aspect, StyleConfig, Word } from "@/lib/api";
import { CaptionPreview, canvasSize } from "@/components/CaptionPreview";

export type PlayerHandle = {
  seek: (ms: number) => void;
  play: () => void;
  pause: () => void;
  toggle: () => void;
  /** Plays [startMs, endMs) once and pauses at the end. */
  playRange: (startMs: number, endMs: number) => void;
  time: () => number;
};

type Props = {
  src: string;
  onTime: (ms: number) => void;
  overlay?: {
    aspect: Aspect;
    cropX: number;
    onCropX: (x: number) => void;
    captions: { words: Word[]; style: StyleConfig } | null;
    active: boolean; // playhead inside the clip
  } | null;
};

export const Player = forwardRef<PlayerHandle, Props>(function Player({ src, onTime, overlay }, ref) {
  const video = useRef<HTMLVideoElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const stopAt = useRef<number | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0, vw: 16, vh: 9 });
  const [t, setT] = useState(0);

  useImperativeHandle(ref, () => ({
    seek: (ms) => {
      if (video.current) video.current.currentTime = Math.max(0, ms / 1000);
    },
    play: () => void video.current?.play(),
    pause: () => video.current?.pause(),
    toggle: () => {
      const v = video.current;
      if (!v) return;
      if (v.paused) void v.play();
      else v.pause();
    },
    playRange: (s, e) => {
      const v = video.current;
      if (!v) return;
      stopAt.current = e;
      v.currentTime = s / 1000;
      void v.play();
    },
    time: () => (video.current?.currentTime || 0) * 1000,
  }));

  // Smooth time updates via rAF while playing (timeupdate is only ~4Hz).
  useEffect(() => {
    let raf = 0;
    let last = -1;
    const tick = () => {
      const v = video.current;
      if (v) {
        const ms = v.currentTime * 1000;
        if (Math.abs(ms - last) > 30) {
          last = ms;
          setT(ms);
          onTime(ms);
        }
        if (stopAt.current !== null && ms >= stopAt.current) {
          v.pause();
          stopAt.current = null;
        }
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [onTime]);

  const measure = useCallback(() => {
    const b = box.current;
    const v = video.current;
    if (!b) return;
    setSize({ w: b.clientWidth, h: b.clientHeight, vw: v?.videoWidth || 16, vh: v?.videoHeight || 9 });
  }, []);
  useEffect(() => {
    measure();
    const ro = new ResizeObserver(measure);
    if (box.current) ro.observe(box.current);
    return () => ro.disconnect();
  }, [measure]);

  // Displayed video rect inside the box (object-contain).
  const scale = Math.min(size.w / size.vw, size.h / size.vh) || 0;
  const dispW = size.vw * scale;
  const dispH = size.vh * scale;
  const offX = (size.w - dispW) / 2;
  const offY = (size.h - dispH) / 2;

  let crop: { x: number; y: number; w: number; h: number } | null = null;
  if (overlay && scale > 0) {
    const [cw, ch] = canvasSize(overlay.aspect);
    const target = cw / ch;
    const src = size.vw / size.vh;
    if (src > target) {
      const h = dispH;
      const w = h * target;
      crop = { x: offX + (dispW - w) * overlay.cropX, y: offY, w, h };
    } else {
      const w = dispW;
      const h = w / target;
      crop = { x: offX, y: offY + (dispH - h) / 2, w, h };
    }
  }

  const drag = useRef<{ startX: number; startCrop: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent) => {
    if (!overlay || !crop || dispW - crop.w < 1) return;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    drag.current = { startX: e.clientX, startCrop: overlay.cropX };
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!drag.current || !overlay || !crop) return;
    const range = dispW - crop.w;
    const nx = drag.current.startCrop + (e.clientX - drag.current.startX) / range;
    overlay.onCropX(Math.round(Math.min(1, Math.max(0, nx)) * 1000) / 1000);
  };

  return (
    <div ref={box} className="relative aspect-video w-full overflow-hidden rounded-lg bg-black">
      <video
        ref={video}
        src={src}
        className="absolute inset-0 h-full w-full object-contain"
        controls
        preload="metadata"
        playsInline
        onLoadedMetadata={measure}
      />
      {crop && overlay && (
        <div
          className="absolute cursor-ew-resize border-2 border-accent2/80"
          style={{ left: crop.x, top: crop.y, width: crop.w, height: crop.h, boxShadow: "0 0 0 9999px rgba(0,0,0,0.55)" }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={() => (drag.current = null)}
          title="Drag to reframe"
        >
          {overlay.active && overlay.captions && (
            <CaptionPreview words={overlay.captions.words} t={t} style={overlay.captions.style} aspect={overlay.aspect} frameH={crop.h} />
          )}
        </div>
      )}
    </div>
  );
});
