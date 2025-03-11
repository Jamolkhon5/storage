DROP INDEX IF EXISTS idx_shares_resource;
DROP INDEX IF EXISTS idx_shares_token;
DROP INDEX IF EXISTS idx_files_folder_id;
DROP INDEX IF EXISTS idx_folders_parent_id;
DROP INDEX IF EXISTS idx_folders_path;
DROP INDEX IF EXISTS idx_files_uuid_owner;
DROP INDEX IF EXISTS idx_file_versions_file_uuid_version;
DROP INDEX IF EXISTS idx_files_deleted_at;
DROP INDEX IF EXISTS idx_folders_deleted_at;
DROP INDEX IF EXISTS idx_storage_quotas_owner_id;

DROP TABLE IF EXISTS file_versions;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS file_previews;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS folders;
DROP TABLE IF EXISTS trash_settings;
DROP TABLE IF EXISTS storage_quotas;

DROP FUNCTION IF EXISTS update_updated_at_column() CASCADE;
DROP FUNCTION IF EXISTS cleanup_trash() CASCADE;
DROP EXTENSION IF EXISTS "uuid-ossp";