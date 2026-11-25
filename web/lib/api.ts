// Typed client for the ClipForge Go API. All calls are same-origin (/api/*).

export type Word = { s: number; e: number; t: string; p?: number };
export type Segment = { s: number; e: number; t: string };

export type Video = {
  id: string;
  sourceUrl: string;
  platform: "youtube" | "twitch";
  title: string;
  channel: string;
  durationMs: number;
  width: number;
  height: number;
  thumbnail: string;
  status: "queued" | "downloading" | "transcribing" | "ready" | "failed";
  progress: number;
  stage: string;
  error: string;
  hasProxy: boolean;
  clipCount: number;
  createdAt: string;
  meta: { chapters?: { start_time: number; end_time: number; title: string }[]; webpageUrl?: string };
};

export type Transcript = { videoId: string; language: string; provider: string; words: Word[]; segments: Segment[] };

export type Aspect = "9:16" | "16:9" | "1:1";

export type Clip = {
  id: string;
  videoId: string;
  startMs: number;
  endMs: number;
  title: string;
  description: string;
  aspect: Aspect;
  cropX: number;
  captionsEnabled: boolean;
  captionStyleId: string | null;
  wordOverrides: Word[];
  source: "manual" | "ai";
  aiScore: number;
  aiReason: string;
  status: "draft" | "queued" | "rendering" | "done" | "failed";
  progress: number;
  error: string;
  hasOutput: boolean;
  renderedAt: string | null;
  updatedAt: string;
};

export type StyleConfig = {
  font: string;
  weight: number;
  size: number;
  primary: string;
  highlight: string;
  outline: string;
  outlineWidth: number;
  shadow: number;
  position: "bottom" | "middle" | "top";
  marginV: number;
  wordsPerLine: number;
  uppercase: boolean;
  animation: "highlight" | "karaoke" | "pop" | "none";
};

export type CaptionStyle = { id: string; name: string; config: StyleConfig; isDefault: boolean };

export type Settings = {
  whisperModel: string;
  whisperLanguage: string;
  transcribeProvider: "local" | "openai" | "groq";
  fallbackProvider: "none" | "openai" | "groq";
  fallbackOverMinutes: number;
  claudeModel: string;
  aiProvider: "auto" | "claude" | "gemini";
  geminiModel: string;
  postizUrl: string;
  suggestMinSec: number;
  suggestMaxSec: number;
  suggestCount: number;
  suggestInstructions: string;
  secrets: Record<"anthropicApiKey" | "openaiApiKey" | "groqApiKey" | "postizApiKey" | "geminiApiKey", string>;
};

export type Integration = { id: string; name: string; identifier: string; picture: string; disabled: boolean };

export type Publication = {
  id: string;
  clipId: string;
  content: string;
  scheduledAt: string | null;
  status: "queued" | "sent" | "failed";
  error: string;
  createdAt: string;
};

export type SystemInfo = { ytdlpVersion: string; whisperModels: string[]; cookiesSet: boolean };

export type ServerEvent = {
  kind: "video" | "clip" | "suggest" | "publication";
  id: string;
  videoId?: string;
  status: string;
  stage?: string;
  progress: number;
  error?: string;
};

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "same-origin",
    headers: { "X-Requested-With": "clipforge" },
  };
  if (body instanceof FormData) {
    init.body = body;
  } else if (body !== undefined) {
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  const res = await fetch(`/api${path}`, init);
  if (res.status === 401 && typeof window !== "undefined" && !path.startsWith("/auth/")) {
    window.location.href = "/login";
    throw new ApiError(401, "unauthorized");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error || res.statusText);
  return data as T;
}

export const api = {
  login: (email: string, password: string) => request<{ email: string }>("POST", "/auth/login", { email, password }),
  logout: () => request("POST", "/auth/logout"),
  me: () => request<{ email: string }>("GET", "/auth/me"),
  system: () => request<SystemInfo>("GET", "/system"),

  videos: () => request<Video[]>("GET", "/videos"),
  video: (id: string) => request<Video>("GET", `/videos/${id}`),
  addVideo: (url: string) => request<Video>("POST", "/videos", { url }),
  deleteVideo: (id: string) => request("DELETE", `/videos/${id}`),
  retryVideo: (id: string) => request("POST", `/videos/${id}/retry`),
  retranscribe: (id: string) => request("POST", `/videos/${id}/transcribe`),
  transcript: (id: string) => request<Transcript>("GET", `/videos/${id}/transcript`),
  proxyUrl: (id: string) => `/api/videos/${id}/proxy`,

  clips: (videoId: string) => request<Clip[]>("GET", `/videos/${videoId}/clips`),
  clip: (id: string) => request<Clip>("GET", `/clips/${id}`),
  createClip: (videoId: string, c: Partial<Clip>) => request<Clip>("POST", `/videos/${videoId}/clips`, c),
  updateClip: (id: string, c: Partial<Clip>) => request<Clip>("PUT", `/clips/${id}`, c),
  deleteClip: (id: string) => request("DELETE", `/clips/${id}`),
  renderClip: (id: string) => request("POST", `/clips/${id}/render`),
  clipFile: (id: string, kind: "mp4" | "srt" | "thumb", download = false, v?: string) =>
    `/api/clips/${id}/file/${kind}?${download ? "download=1&" : ""}v=${encodeURIComponent(v || "")}`,

  suggest: (videoId: string, replace: boolean, instructions: string) =>
    request("POST", `/videos/${videoId}/suggest`, { replace, instructions }),
  suggestStatus: (videoId: string) =>
    request<{ status: string; error?: string; count?: number }>("GET", `/videos/${videoId}/suggest`),

  styles: () => request<CaptionStyle[]>("GET", "/styles"),
  saveStyle: (s: Partial<CaptionStyle>) =>
    s.id ? request<CaptionStyle>("PUT", `/styles/${s.id}`, s) : request<CaptionStyle>("POST", "/styles", s),
  deleteStyle: (id: string) => request("DELETE", `/styles/${id}`),

  settings: () => request<Settings>("GET", "/settings"),
  saveSettings: (s: Record<string, unknown>) => request<Settings>("PUT", "/settings", s),
  uploadCookies: (file: File) => {
    const fd = new FormData();
    fd.append("file", file);
    return request<{ ok: boolean; youtubeCookies: number; loggedIn: boolean; warning?: string }>("POST", "/settings/cookies", fd);
  },
  youtubeTest: () => request<{ ok: boolean; cookiesUsed: boolean; title?: string; error?: string }>("POST", "/settings/youtube-test"),
  deleteCookies: () => request("DELETE", "/settings/cookies"),

  integrations: () => request<{ configured: boolean; integrations: Integration[] }>("GET", "/postiz/integrations"),
  publications: (clipId: string) => request<Publication[]>("GET", `/clips/${clipId}/publications`),
  publish: (
    clipId: string,
    body: {
      targets: { integrationId: string; identifier: string; settings?: Record<string, unknown> }[];
      content: string;
      scheduledAt: string | null;
      draft: boolean;
    },
  ) => request<Publication>("POST", `/clips/${clipId}/publish`, body),
};
