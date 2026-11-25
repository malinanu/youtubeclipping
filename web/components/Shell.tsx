"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Clapperboard, LogOut, Palette, Settings } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/util";

export function Shell({ children, wide }: { children: React.ReactNode; wide?: boolean }) {
  const path = usePathname();
  const nav = [
    { href: "/", label: "Videos", icon: Clapperboard },
    { href: "/styles", label: "Caption styles", icon: Palette },
    { href: "/settings", label: "Settings", icon: Settings },
  ];
  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-30 border-b border-line bg-bg/90 backdrop-blur">
        <div className={cn("mx-auto flex h-14 items-center gap-6 px-4", wide ? "max-w-[1800px]" : "max-w-6xl")}>
          <Link href="/" className="flex items-center gap-2 font-bold tracking-tight">
            <span className="grid h-7 w-7 place-items-center rounded-md bg-accent text-white">✂</span>
            ClipForge
          </Link>
          <nav className="flex gap-1">
            {nav.map((n) => (
              <Link
                key={n.href}
                href={n.href}
                className={cn(
                  "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm",
                  (n.href === "/" ? path === "/" || path.startsWith("/videos") : path.startsWith(n.href))
                    ? "bg-panel2 text-white"
                    : "text-muted hover:text-white",
                )}
              >
                <n.icon size={15} /> {n.label}
              </Link>
            ))}
          </nav>
          <button
            className="ml-auto flex items-center gap-1.5 text-sm text-muted hover:text-white"
            onClick={async () => {
              await api.logout().catch(() => {});
              window.location.href = "/login";
            }}
          >
            <LogOut size={15} /> Sign out
          </button>
        </div>
      </header>
      <main className={cn("mx-auto px-4 py-6", wide ? "max-w-[1800px]" : "max-w-6xl")}>{children}</main>
    </div>
  );
}

export function ProgressBar({ value, className }: { value: number; className?: string }) {
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-line", className)}>
      <div className="h-full rounded-full bg-accent transition-all" style={{ width: `${Math.round(Math.min(1, Math.max(0, value)) * 100)}%` }} />
    </div>
  );
}

export function StatusBadge({ status }: { status: string }) {
  const color: Record<string, string> = {
    ready: "bg-emerald-500/15 text-emerald-300",
    done: "bg-emerald-500/15 text-emerald-300",
    sent: "bg-emerald-500/15 text-emerald-300",
    failed: "bg-red-500/15 text-red-300",
    downloading: "bg-sky-500/15 text-sky-300",
    uploading: "bg-sky-500/15 text-sky-300",
    transcribing: "bg-violet-500/15 text-violet-300",
    rendering: "bg-amber-500/15 text-amber-300",
    queued: "bg-zinc-500/20 text-zinc-300",
    draft: "bg-zinc-500/20 text-zinc-300",
  };
  return <span className={cn("rounded px-1.5 py-0.5 text-[11px] font-semibold uppercase", color[status] || "bg-zinc-700 text-zinc-200")}>{status}</span>;
}
