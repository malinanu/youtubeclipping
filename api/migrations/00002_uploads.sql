-- +goose Up
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_platform_check;
ALTER TABLE videos ADD CONSTRAINT videos_platform_check CHECK (platform IN ('youtube','twitch','upload'));

-- +goose Down
DELETE FROM videos WHERE platform = 'upload';
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_platform_check;
ALTER TABLE videos ADD CONSTRAINT videos_platform_check CHECK (platform IN ('youtube','twitch'));
