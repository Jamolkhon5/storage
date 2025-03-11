-- Удаляем триггер и функцию для обновления updated_at
DROP TRIGGER IF EXISTS update_trash_settings_updated_at ON trash_settings;
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Удаляем функцию очистки корзины
DROP FUNCTION IF EXISTS cleanup_trash();

-- Удаляем индексы
DROP INDEX IF EXISTS idx_files_deleted_at;
DROP INDEX IF EXISTS idx_folders_deleted_at;

-- Удаляем таблицу настроек корзины
DROP TABLE IF EXISTS trash_settings;