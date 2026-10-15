-- Moderation, slice 5: removed things don't keep their words forever.
-- 30 days after the admin removes a post or comment (long enough for
-- appeals and second thoughts), its text is erased, like a deletion: the
-- row stays so threads keep their shape, but the words are gone. Handled
-- reports are deleted after 30 days too, and a finished suspension's note
-- and appeal are emptied. store/retention.go does it, hourly.

-- +goose Up
ALTER TABLE posts ADD COLUMN erased_at timestamptz;
ALTER TABLE comments ADD COLUMN erased_at timestamptz;

ALTER TABLE posts ADD CONSTRAINT erased_post_is_empty
    CHECK (erased_at IS NULL OR (body = '' AND url IS NULL));
ALTER TABLE comments ADD CONSTRAINT erased_comment_is_empty
    CHECK (erased_at IS NULL OR body = '');

-- A removed comment may now have an empty body (once erased).
ALTER TABLE comments DROP CONSTRAINT comment_body_length;
ALTER TABLE comments ADD CONSTRAINT comment_body_length
    CHECK (char_length(body) <= 10000
           AND (deleted_at IS NOT NULL OR erased_at IS NOT NULL OR char_length(body) >= 1));

-- The hourly sweep finds its work through these (small: only removed,
-- not-yet-erased rows, and handled reports).
CREATE INDEX posts_to_erase_idx ON posts (removed_at) WHERE removed_at IS NOT NULL AND erased_at IS NULL;
CREATE INDEX comments_to_erase_idx ON comments (removed_at) WHERE removed_at IS NOT NULL AND erased_at IS NULL;
CREATE INDEX reports_handled_idx ON reports (handled_at) WHERE handled_at IS NOT NULL;

-- +goose Down
-- (Fails if any comment has been erased: its empty body can't go back.)
DROP INDEX reports_handled_idx;
DROP INDEX comments_to_erase_idx;
DROP INDEX posts_to_erase_idx;
ALTER TABLE comments DROP CONSTRAINT comment_body_length;
ALTER TABLE comments ADD CONSTRAINT comment_body_length
    CHECK (char_length(body) <= 10000
           AND (deleted_at IS NOT NULL OR char_length(body) >= 1));
ALTER TABLE comments DROP CONSTRAINT erased_comment_is_empty;
ALTER TABLE posts DROP CONSTRAINT erased_post_is_empty;
ALTER TABLE comments DROP COLUMN erased_at;
ALTER TABLE posts DROP COLUMN erased_at;
