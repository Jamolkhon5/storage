DROP INDEX IF EXISTS idx_files_context_type;
ALTER TABLE files DROP COLUMN IF EXISTS context_type;