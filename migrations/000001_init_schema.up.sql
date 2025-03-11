CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE folders (
                         id SERIAL PRIMARY KEY,
                         name VARCHAR(255) NOT NULL,
                         owner_id VARCHAR(255) NOT NULL,
                         parent_id INTEGER REFERENCES folders(id),
                         path TEXT NOT NULL DEFAULT '/',
                         level INTEGER NOT NULL DEFAULT 0,
                         size_bytes BIGINT DEFAULT 0,
                         files_count INTEGER DEFAULT 0,
                         created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                         updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                         deleted_at TIMESTAMP WITH TIME ZONE,
                         restore_path TEXT,
                         restore_parent_id INTEGER,
                         CONSTRAINT unique_folder_name UNIQUE (parent_id, name, owner_id)
);

CREATE TABLE files (
                       uuid UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
                       name VARCHAR(255) NOT NULL,
                       mime_type VARCHAR(255) NOT NULL,
                       size_bytes BIGINT NOT NULL,
                       folder_id INTEGER REFERENCES folders(id),
                       owner_id VARCHAR(255) NOT NULL,
                       created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                       updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                       deleted_at TIMESTAMP WITH TIME ZONE,
                       restore_path TEXT,
                       restore_folder_id INTEGER,
                       current_version INTEGER DEFAULT 1
);

CREATE TABLE shares (
                        id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
                        resource_id TEXT NOT NULL,
                        resource_type VARCHAR(10) NOT NULL CHECK (resource_type IN ('file', 'folder')),
                        owner_id VARCHAR(255) NOT NULL,
                        access_type VARCHAR(20) NOT NULL CHECK (access_type IN ('view', 'edit', 'full')),
                        token TEXT NOT NULL UNIQUE,
                        expires_at TIMESTAMP WITH TIME ZONE,
                        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                        user_ids TEXT NOT NULL DEFAULT ''
);

CREATE TABLE file_previews (
                               file_uuid UUID REFERENCES files(uuid),
                               preview_data BYTEA,
                               mime_type VARCHAR(255),
                               created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                               PRIMARY KEY (file_uuid)
);

CREATE TABLE file_versions (
                               id SERIAL PRIMARY KEY,
                               file_uuid UUID REFERENCES files(uuid),
                               version_number INTEGER NOT NULL,
                               s3_key TEXT NOT NULL,
                               size_bytes BIGINT NOT NULL,
                               created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                               deleted_at TIMESTAMP WITH TIME ZONE,
                               UNIQUE(file_uuid, version_number)
);

CREATE TABLE trash_settings (
                                id SERIAL PRIMARY KEY,
                                owner_id VARCHAR(255) NOT NULL UNIQUE,
                                retention_period INTERVAL NOT NULL DEFAULT '1 hour',
                                created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Удаляем старое ограничение и создаем новое
ALTER TABLE files DROP CONSTRAINT IF EXISTS unique_file_name;
CREATE UNIQUE INDEX unique_active_file_name ON files (folder_id, name, owner_id)
    WHERE deleted_at IS NULL;

-- Создаем остальные индексы
CREATE INDEX idx_folders_path ON folders(path);
CREATE INDEX idx_folders_parent_id ON folders(parent_id);
CREATE INDEX idx_files_folder_id ON files(folder_id);
CREATE INDEX idx_shares_token ON shares(token);
CREATE INDEX idx_shares_resource ON shares(resource_id, resource_type);
CREATE INDEX idx_file_previews_uuid ON file_previews(file_uuid);
CREATE INDEX idx_files_uuid_owner ON files(uuid, owner_id);
CREATE INDEX idx_file_versions_file_uuid_version ON file_versions(file_uuid, version_number);
CREATE INDEX idx_files_deleted_at ON files(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_folders_deleted_at ON folders(deleted_at) WHERE deleted_at IS NOT NULL;