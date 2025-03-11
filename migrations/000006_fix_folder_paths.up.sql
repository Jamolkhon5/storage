-- Исправляем все пути с двойными слешами
UPDATE folders
SET path = REPLACE(path, '//', '/')
WHERE path LIKE '//%';

-- Обновляем пути подпапок
WITH RECURSIVE folder_tree AS (
    -- Корневые папки
    SELECT id, name, parent_id, path
    FROM folders
    WHERE parent_id IS NULL

    UNION ALL

    -- Подпапки
    SELECT f.id, f.name, f.parent_id, f.path
    FROM folders f
             JOIN folder_tree ft ON f.parent_id = ft.id
)
UPDATE folders f
SET path = (
    SELECT
        CASE
            WHEN p.parent_id IS NULL THEN '/' || f.name
            ELSE p.path || '/' || f.name
            END
    FROM folders p
    WHERE p.id = f.parent_id
)
WHERE f.parent_id IS NOT NULL
  AND f.path LIKE '//%'; -- Только для папок с неправильным путем