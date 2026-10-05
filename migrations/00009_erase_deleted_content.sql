-- +goose Up

-- Deleting a post or comment ERASES its content in place: the row stays, so
-- threads keep their shape (and a post keeps its title, so the discussion
-- under it still makes sense), but the words themselves are gone from the
-- database, not just hidden. These constraints make that a guarantee.

-- A deleted post keeps its title, but its body and link must be empty.
ALTER TABLE posts ADD CONSTRAINT deleted_post_is_erased
    CHECK (deleted_at IS NULL OR (body = '' AND url IS NULL));

-- Comments: a live comment needs at least one character; a deleted one must
-- have none.
ALTER TABLE comments DROP CONSTRAINT comment_body_length;
ALTER TABLE comments ADD CONSTRAINT comment_body_length
    CHECK (char_length(body) <= 10000
           AND (deleted_at IS NOT NULL OR char_length(body) >= 1));
ALTER TABLE comments ADD CONSTRAINT deleted_comment_is_erased
    CHECK (deleted_at IS NULL OR body = '');

-- +goose Down

ALTER TABLE comments DROP CONSTRAINT deleted_comment_is_erased;
ALTER TABLE comments DROP CONSTRAINT comment_body_length;
ALTER TABLE comments ADD CONSTRAINT comment_body_length
    CHECK (char_length(body) BETWEEN 1 AND 10000);  -- fails if erased comments exist
ALTER TABLE posts DROP CONSTRAINT deleted_post_is_erased;
