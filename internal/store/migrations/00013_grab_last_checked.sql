-- +goose Up
-- Tracks when a grab was last actually checked against the download
-- client (via the /downloads/{hash}/completed webhook or a fallback
-- poll) - NULL means never checked, so a brand-new grab is immediately
-- eligible for the first fallback poll rather than needing to wait out a
-- full grace period with no data at all. See DownloadService.RefreshQueue.
ALTER TABLE grabs ADD COLUMN last_checked TIMESTAMP;

-- +goose Down
ALTER TABLE grabs DROP COLUMN last_checked;
