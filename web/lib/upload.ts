"use client";

// Chunked, resumable uploads to /api/uploads. Each chunk is a separate request
// (8 MiB by default) so reverse-proxy body limits and flaky connections don't matter.

export type UploadProgress = {
  sent: number;
  total: number;
  bytesPerSec: number;
  etaSec: number;
};

type Resume = { id: string; name: string; size: number; lastModified: number };
const KEY = "clipforge-uploads";

function loadResumes(): Resume[] {
  try {
    return JSON.parse(localStorage.getItem(KEY) || "[]");
  } catch {
    return [];
  }
}

function saveResumes(r: Resume[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(r.slice(-20)));
  } catch {
    /* storage unavailable: resume just won't survive a reload */
  }
}

const sameFile = (r: Resume, f: File) => r.name === f.name && r.size === f.size && r.lastModified === f.lastModified;

async function json<T>(res: Response): Promise<T> {
  const data = await res.json().catch(() => ({}));
  if (!res.ok && res.status !== 409) throw new Error((data as { error?: string }).error || res.statusText);
  return data as T;
}

const headers = { "X-Requested-With": "clipforge" };

export class Upload {
  id = "";
  private aborted = false;
  private ctrl: AbortController | null = null;

  constructor(
    private file: File,
    private onProgress: (p: UploadProgress) => void,
  ) {}

  /** Starts (or resumes) the upload and resolves with the video id when processing is queued. */
  async run(): Promise<string> {
    const f = this.file;
    let received = 0;
    let chunkSize = 8 << 20;

    // Resume a previous upload of the same file if the server still has it.
    const prev = loadResumes().find((r) => sameFile(r, f));
    if (prev) {
      const res = await fetch(`/api/uploads/${prev.id}`, { headers, credentials: "same-origin" });
      if (res.ok) {
        const st = await res.json();
        if (st.status === "uploading") {
          this.id = prev.id;
          received = st.received;
          chunkSize = st.chunkSize || chunkSize;
        }
      }
    }
    if (!this.id) {
      const st = await json<{ id: string; chunkSize: number }>(
        await fetch("/api/uploads", {
          method: "POST",
          credentials: "same-origin",
          headers: { ...headers, "Content-Type": "application/json" },
          body: JSON.stringify({ filename: f.name, size: f.size }),
        }),
      );
      this.id = st.id;
      chunkSize = st.chunkSize || chunkSize;
      saveResumes([...loadResumes().filter((r) => !sameFile(r, f)), { id: st.id, name: f.name, size: f.size, lastModified: f.lastModified }]);
    }

    const started = performance.now();
    const startBytes = received;
    const report = () => {
      const secs = Math.max(0.001, (performance.now() - started) / 1000);
      const rate = (received - startBytes) / secs;
      this.onProgress({ sent: received, total: f.size, bytesPerSec: rate, etaSec: rate > 0 ? (f.size - received) / rate : 0 });
    };
    report();

    let failures = 0;
    while (received < f.size) {
      if (this.aborted) throw new Error("Upload cancelled");
      const end = Math.min(received + chunkSize, f.size);
      this.ctrl = new AbortController();
      try {
        const res = await fetch(`/api/uploads/${this.id}?offset=${received}`, {
          method: "PUT",
          credentials: "same-origin",
          headers: { ...headers, "Content-Type": "application/octet-stream" },
          body: f.slice(received, end),
          signal: this.ctrl.signal,
        });
        const st = await json<{ received: number }>(res);
        // 200 = appended; 409 = server is elsewhere (lost response / resume): jump there.
        received = st.received;
        failures = 0;
        report();
      } catch (e) {
        if (this.aborted) throw new Error("Upload cancelled");
        failures++;
        if (failures > 5) throw e;
        await new Promise((r) => setTimeout(r, Math.min(30_000, 1000 * 2 ** failures)));
        // Ask the server where it is before retrying.
        const st = await fetch(`/api/uploads/${this.id}`, { headers, credentials: "same-origin" })
          .then((r) => r.json())
          .catch(() => null);
        if (st && typeof st.received === "number") received = st.received;
      }
    }

    await json(await fetch(`/api/uploads/${this.id}/complete`, { method: "POST", credentials: "same-origin", headers }));
    saveResumes(loadResumes().filter((r) => !sameFile(r, f)));
    return this.id;
  }

  /** Stops the upload and deletes the partial video on the server. */
  async cancel() {
    this.aborted = true;
    this.ctrl?.abort();
    saveResumes(loadResumes().filter((r) => !sameFile(r, this.file)));
    if (this.id) await fetch(`/api/videos/${this.id}`, { method: "DELETE", credentials: "same-origin", headers }).catch(() => {});
  }
}

export function fmtBytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(2)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  return `${Math.max(0, Math.round(n / 1024))} KB`;
}
