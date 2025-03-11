-- 000002_add_storage_quotas.up.sql
CREATE TABLE storage_quotas (
                                id SERIAL PRIMARY KEY,
                                owner_id VARCHAR(255) NOT NULL UNIQUE,
                                total_bytes_limit BIGINT NOT NULL DEFAULT 5368709120, -- 5GB в байтах
                                used_bytes BIGINT NOT NULL DEFAULT 0,
                                created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Индекс для быстрого поиска по owner_id
CREATE INDEX idx_storage_quotas_owner_id ON storage_quotas(owner_id);

-- Триггер для автоматического обновления updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_storage_quotas_updated_at
    BEFORE UPDATE ON storage_quotas
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();