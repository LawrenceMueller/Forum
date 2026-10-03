-- +goose Up

CREATE TABLE comments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- Random, like posts, for permalinks (/p/<post>#c-<id>) and
    -- "continue this thread" pages, without exposing a countable sequence.
    public_id   text NOT NULL UNIQUE CONSTRAINT comment_public_id_format CHECK (public_id ~ '^[a-z2-7]{8}$'),

    post_id     bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    -- NULL for a top-level comment; otherwise the comment being replied to.
    parent_id   bigint REFERENCES comments(id) ON DELETE CASCADE,
    author_id   bigint NOT NULL REFERENCES users(id),

    -- 0 for top-level, parent's depth + 1 for replies. Stored so the reply
    -- rules don't need to walk up the tree.
    depth       integer NOT NULL DEFAULT 0 CONSTRAINT depth_not_negative CHECK (depth >= 0),

    body        text NOT NULL CONSTRAINT comment_body_length CHECK (char_length(body) BETWEEN 1 AND 10000),

    created_at  timestamptz NOT NULL DEFAULT now(),
    edited_at   timestamptz,

    -- Counted separately (not just their difference) because the "best"
    -- sort needs both: 10 up / 1 down is better than 40 up / 20 down.
    upvotes     integer NOT NULL DEFAULT 0,
    downvotes   integer NOT NULL DEFAULT 0,

    content_hash text NOT NULL,
    deleted_at  timestamptz,   -- deleted by its author
    removed_at  timestamptz    -- removed by the admin
);

CREATE INDEX comments_post_id_idx   ON comments (post_id);
CREATE INDEX comments_parent_id_idx ON comments (parent_id);
CREATE INDEX comments_author_id_idx ON comments (author_id);

-- +goose Down

DROP TABLE comments;
