ALTER TABLE files
    ADD COLUMN context_type VARCHAR(50) NULL; -- Явно указываем, что поле может быть NULL

CREATE INDEX idx_files_context_type ON files(context_type);