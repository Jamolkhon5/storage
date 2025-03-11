-- Создаем таблицу для настроек корзины если её нет
CREATE TABLE IF NOT EXISTS trash_settings (
                                              id SERIAL PRIMARY KEY,
                                              owner_id VARCHAR(255) NOT NULL UNIQUE,
    retention_period INTERVAL NOT NULL DEFAULT '1 hour',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                             );

-- Создаем индексы для оптимизации запросов если их нет
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE indexname = 'idx_files_deleted_at'
    ) THEN
CREATE INDEX idx_files_deleted_at ON files(deleted_at) WHERE deleted_at IS NOT NULL;
END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE indexname = 'idx_folders_deleted_at'
    ) THEN
CREATE INDEX idx_folders_deleted_at ON folders(deleted_at) WHERE deleted_at IS NOT NULL;
END IF;
END $$;

-- Создаем триггерную функцию для обновления updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
RETURN NEW;
END;
$$ language 'plpgsql';

-- Добавляем триггер для таблицы trash_settings если его нет
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'update_trash_settings_updated_at'
    ) THEN
CREATE TRIGGER update_trash_settings_updated_at
    BEFORE UPDATE ON trash_settings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
END IF;
END $$;

-- Создаем функцию для автоматической очистки корзины
CREATE OR REPLACE FUNCTION cleanup_trash()
RETURNS void AS $$
DECLARE
setting record;
BEGIN
FOR setting IN SELECT * FROM trash_settings LOOP
-- Удаляем файлы, которые находятся в корзине дольше установленного периода
DELETE FROM files
WHERE deleted_at IS NOT NULL
  AND deleted_at + setting.retention_period < CURRENT_TIMESTAMP
  AND owner_id = setting.owner_id;

-- Удаляем папки, которые находятся в корзине дольше установленного периода
DELETE FROM folders
WHERE deleted_at IS NOT NULL
  AND deleted_at + setting.retention_period < CURRENT_TIMESTAMP
  AND owner_id = setting.owner_id;
END LOOP;
END;
$$ LANGUAGE plpgsql;