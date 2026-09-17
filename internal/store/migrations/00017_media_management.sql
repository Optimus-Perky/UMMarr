-- +goose Up
-- Radarr/Sonarr/Lidarr-style media management settings, shared by movies, TV
-- and music. Every default keeps UMMarr behaving as it did before these
-- settings existed. propers_repacks, analyze_video_files,
-- import_using_script/import_script_path, rescan_after_refresh and the
-- recycle_bin_* columns are stored and shown on the settings page, but
-- nothing acts on them yet - each depends on a feature UMMarr doesn't have.
CREATE TABLE media_settings (
    id                          INTEGER PRIMARY KEY CHECK (id = 1),
    replace_illegal_characters  BOOLEAN NOT NULL DEFAULT 0,
    colon_replacement           TEXT    NOT NULL DEFAULT 'delete',
    create_empty_folders        BOOLEAN NOT NULL DEFAULT 0,
    delete_empty_folders        BOOLEAN NOT NULL DEFAULT 0,
    skip_free_space_check       BOOLEAN NOT NULL DEFAULT 0,
    minimum_free_space_mb       INTEGER NOT NULL DEFAULT 100,
    use_hardlinks               BOOLEAN NOT NULL DEFAULT 0,
    import_extra_files          BOOLEAN NOT NULL DEFAULT 0,
    extra_file_extensions       TEXT    NOT NULL DEFAULT 'srt',
    unmonitor_deleted           BOOLEAN NOT NULL DEFAULT 0,
    movie_file_date             TEXT    NOT NULL DEFAULT 'none',
    episode_file_date           TEXT    NOT NULL DEFAULT 'none',
    track_file_date             TEXT    NOT NULL DEFAULT 'none',
    set_permissions             BOOLEAN NOT NULL DEFAULT 0,
    chmod_folder                TEXT    NOT NULL DEFAULT '755',
    chown_group                 TEXT    NOT NULL DEFAULT '',
    propers_repacks             TEXT    NOT NULL DEFAULT 'do_not_prefer',
    analyze_video_files         BOOLEAN NOT NULL DEFAULT 1,
    import_using_script         BOOLEAN NOT NULL DEFAULT 0,
    import_script_path          TEXT    NOT NULL DEFAULT '',
    rescan_after_refresh        TEXT    NOT NULL DEFAULT 'always',
    recycle_bin_path            TEXT    NOT NULL DEFAULT '',
    recycle_bin_cleanup_days    INTEGER NOT NULL DEFAULT 7
);
INSERT INTO media_settings (id) VALUES (1);

-- Per media type, like Radarr's Rename Movies / Sonarr's Rename Episodes.
ALTER TABLE naming_config ADD COLUMN rename_files BOOLEAN NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE naming_config DROP COLUMN rename_files;
DROP TABLE media_settings;
