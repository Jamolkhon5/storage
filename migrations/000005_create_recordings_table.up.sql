-- 000005_create_recordings_table.up.sql - обновленная версия
-- Создаем таблицу recordings
CREATE TABLE IF NOT EXISTS recordings (
                                          file_uuid UUID PRIMARY KEY REFERENCES files(uuid),
    room_id TEXT NOT NULL,
    egress_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    folder_id BIGINT NOT NULL REFERENCES folders(id),
    s3_path TEXT,
    verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
                             );

-- Создаем индексы для recordings
CREATE INDEX IF NOT EXISTS idx_recordings_user_id ON recordings(user_id);
CREATE INDEX IF NOT EXISTS idx_recordings_room_id ON recordings(room_id);

-- Добавляем поле metadata в таблицу folders, если оно не существует
DO $$
BEGIN
    -- Проверяем существование колонки metadata
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'folders'
        AND column_name = 'metadata'
    ) THEN
        -- Если колонка не существует, добавляем её
ALTER TABLE folders ADD COLUMN metadata JSONB DEFAULT '{}'::jsonb;
-- Создаем индекс для metadata
CREATE INDEX idx_folders_metadata ON folders USING gin(metadata);
END IF;
END $$;

