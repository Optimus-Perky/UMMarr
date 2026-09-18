-- +goose Up
-- Quality profiles are per media kind now. The catalog they were built
-- from - WEBDL-1080p, Bluray-2160p - describes video and says nothing
-- about a FLAC, so music was being judged against rows that could never
-- match. An audio profile is built from the audio catalog instead: MP3 at
-- its bitrates, AAC, ALAC, FLAC, FLAC 24bit, WAV.
ALTER TABLE quality_profiles ADD COLUMN media_kind TEXT NOT NULL DEFAULT 'video'
    CHECK (media_kind IN ('video', 'audio'));

-- One audio profile to start with, everything allowed and weighted worst
-- to best, so music has something to point at the moment this lands.
INSERT INTO quality_profiles (name, media_kind, upgrade_allowed, cutoff_quality, items)
VALUES ('Any (audio)', 'audio', 1, '', json_array(
    json_object('quality', 'Unknown',    'weight', 0,  'allowed', json('true')),
    json_object('quality', 'MP3-128',    'weight', 1,  'allowed', json('true')),
    json_object('quality', 'MP3-192',    'weight', 2,  'allowed', json('true')),
    json_object('quality', 'MP3-256',    'weight', 3,  'allowed', json('true')),
    json_object('quality', 'MP3-320',    'weight', 4,  'allowed', json('true')),
    json_object('quality', 'MP3-VBR',    'weight', 5,  'allowed', json('true')),
    json_object('quality', 'AAC',        'weight', 6,  'allowed', json('true')),
    json_object('quality', 'Vorbis',     'weight', 7,  'allowed', json('true')),
    json_object('quality', 'Opus',       'weight', 8,  'allowed', json('true')),
    json_object('quality', 'ALAC',       'weight', 9,  'allowed', json('true')),
    json_object('quality', 'FLAC',       'weight', 10, 'allowed', json('true')),
    json_object('quality', 'FLAC-24bit', 'weight', 11, 'allowed', json('true')),
    json_object('quality', 'WAV',        'weight', 12, 'allowed', json('true'))
));

-- Every artist was on a video profile, because there was nothing else.
UPDATE artists SET quality_profile_id = (SELECT id FROM quality_profiles WHERE media_kind = 'audio' ORDER BY id LIMIT 1);

-- +goose Down
ALTER TABLE quality_profiles DROP COLUMN media_kind;
