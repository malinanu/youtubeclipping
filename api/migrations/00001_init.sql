-- +goose Up
-- Requires PostgreSQL 13+ (gen_random_uuid is built in; no superuser-only extensions).

CREATE TABLE videos (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url   TEXT NOT NULL,
    platform     TEXT NOT NULL CHECK (platform IN ('youtube','twitch')),
    title        TEXT NOT NULL DEFAULT '',
    channel      TEXT NOT NULL DEFAULT '',
    duration_ms  BIGINT NOT NULL DEFAULT 0,
    width        INT NOT NULL DEFAULT 0,
    height       INT NOT NULL DEFAULT 0,
    thumbnail    TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'queued',  -- queued|downloading|transcribing|ready|failed
    progress     REAL NOT NULL DEFAULT 0,
    stage        TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    proxy_path   TEXT NOT NULL DEFAULT '',
    audio_path   TEXT NOT NULL DEFAULT '',
    meta         JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX videos_created_idx ON videos (created_at DESC);

CREATE TABLE transcripts (
    video_id   UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    language   TEXT NOT NULL DEFAULT '',
    provider   TEXT NOT NULL DEFAULT '',
    words      JSONB NOT NULL DEFAULT '[]',
    segments   JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE caption_styles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    config     JSONB NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE clips (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id         UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    start_ms         BIGINT NOT NULL,
    end_ms           BIGINT NOT NULL,
    title            TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    aspect           TEXT NOT NULL DEFAULT '9:16' CHECK (aspect IN ('9:16','16:9','1:1')),
    crop_x           REAL NOT NULL DEFAULT 0.5,
    captions_enabled BOOLEAN NOT NULL DEFAULT true,
    caption_style_id UUID REFERENCES caption_styles(id) ON DELETE SET NULL,
    word_overrides   JSONB NOT NULL DEFAULT '[]',
    source           TEXT NOT NULL DEFAULT 'manual', -- manual|ai
    ai_score         REAL NOT NULL DEFAULT 0,
    ai_reason        TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'draft', -- draft|queued|rendering|done|failed
    progress         REAL NOT NULL DEFAULT 0,
    error            TEXT NOT NULL DEFAULT '',
    output_path      TEXT NOT NULL DEFAULT '',
    srt_path         TEXT NOT NULL DEFAULT '',
    thumb_path       TEXT NOT NULL DEFAULT '',
    rendered_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_ms > start_ms)
);
CREATE INDEX clips_video_idx ON clips (video_id, start_ms);

CREATE TABLE publications (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clip_id        UUID NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
    integrations   JSONB NOT NULL DEFAULT '[]',
    content        TEXT NOT NULL DEFAULT '',
    scheduled_at   TIMESTAMPTZ,
    status         TEXT NOT NULL DEFAULT 'queued', -- queued|sent|failed
    postiz_response JSONB NOT NULL DEFAULT '{}',
    error          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    id         INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    data       JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO settings (id, data) VALUES (1, '{}');

INSERT INTO caption_styles (name, is_default, config) VALUES
('Bold Pop', true, '{"font":"Montserrat","weight":900,"size":78,"primary":"#FFFFFF","highlight":"#FFE81F","outline":"#000000","outlineWidth":6,"shadow":2,"position":"bottom","marginV":420,"wordsPerLine":3,"uppercase":true,"animation":"highlight"}'),
('Clean Karaoke', false, '{"font":"Inter","weight":800,"size":64,"primary":"#FFFFFF","highlight":"#22D3EE","outline":"#000000","outlineWidth":4,"shadow":0,"position":"bottom","marginV":360,"wordsPerLine":5,"uppercase":false,"animation":"karaoke"}'),
('Minimal', false, '{"font":"Inter","weight":600,"size":52,"primary":"#FFFFFF","highlight":"#FFFFFF","outline":"#000000","outlineWidth":3,"shadow":1,"position":"bottom","marginV":120,"wordsPerLine":7,"uppercase":false,"animation":"none"}'),
('Headline Center', false, '{"font":"Bebas Neue","weight":400,"size":110,"primary":"#FFFFFF","highlight":"#FF3B30","outline":"#000000","outlineWidth":7,"shadow":3,"position":"middle","marginV":0,"wordsPerLine":2,"uppercase":true,"animation":"highlight"}');

-- +goose Down
DROP TABLE publications;
DROP TABLE clips;
DROP TABLE caption_styles;
DROP TABLE transcripts;
DROP TABLE videos;
DROP TABLE settings;
