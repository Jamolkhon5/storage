-- Удаляем индексы
DROP INDEX IF EXISTS idx_recordings_user_id;
DROP INDEX IF EXISTS idx_recordings_room_id;
DROP INDEX IF EXISTS idx_folders_metadata;

-- Удаляем таблицу recordings
DROP TABLE IF EXISTS recordings;

-- Удаляем колонку metadata из таблицы folders
ALTER TABLE folders DROP COLUMN IF EXISTS metadata;

ALTER TABLE recordings DROP COLUMN IF EXISTS verified;
ALTER TABLE recordings DROP COLUMN IF EXISTS s3_path;

