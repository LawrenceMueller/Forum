-- +goose Up

-- Admins are made by hand, in psql, never through the site:
--   UPDATE users SET is_admin = true WHERE username = '...';
ALTER TABLE users ADD COLUMN is_admin boolean NOT NULL DEFAULT false;

-- The curated tag list. New tags are added by the admin, by migration or psql.
CREATE TABLE tags (
    id    integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name  text NOT NULL UNIQUE CONSTRAINT tag_name_format CHECK (name ~ '^[a-z0-9-]{1,24}$')
);

INSERT INTO tags (name) VALUES
    ('art'), ('ask'), ('books'), ('diy'), ('food'),
    ('meta'), ('music'), ('outdoors'), ('show'), ('tech');

CREATE TABLE posts (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- What appears in URLs (/p/k7qm3xat). Random, so nobody can walk
    -- /p/1, /p/2, /p/3... to scrape every post, or tell how busy the site is.
    public_id        text NOT NULL UNIQUE CONSTRAINT public_id_format CHECK (public_id ~ '^[a-z2-7]{8}$'),

    -- No ON DELETE: accounts are soft-deleted, so their rows (and posts) stay.
    author_id        bigint NOT NULL REFERENCES users(id),

    title            text NOT NULL CONSTRAINT title_length CHECK (char_length(title) BETWEEN 1 AND 200),
    url              text CONSTRAINT url_length CHECK (url IS NULL OR char_length(url) <= 2000),
    body             text NOT NULL DEFAULT '' CONSTRAINT body_length CHECK (char_length(body) <= 40000),  -- Markdown source

    created_at       timestamptz NOT NULL DEFAULT now(),
    edited_at        timestamptz,                         -- set by edits after the grace period
    last_activity_at timestamptz NOT NULL DEFAULT now(),  -- for bump order ("active")

    score            integer NOT NULL DEFAULT 0,          -- kept in step with votes (later)
    comment_count    integer NOT NULL DEFAULT 0,          -- kept in step with comments (later)
    content_hash     text NOT NULL,                       -- for spotting duplicate posts

    deleted_at       timestamptz,                         -- deleted by its author
    removed_at       timestamptz                          -- removed by the admin
);

CREATE INDEX posts_created_at_idx       ON posts (created_at DESC);
CREATE INDEX posts_last_activity_at_idx ON posts (last_activity_at DESC);
CREATE INDEX posts_author_id_idx        ON posts (author_id);
CREATE INDEX posts_content_hash_idx     ON posts (content_hash);

-- Which tags each post has. "1 to 3 per post" is enforced in Go, inside the
-- same transaction that creates the post (a CHECK can't count rows).
CREATE TABLE post_tags (
    post_id  bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id   integer NOT NULL REFERENCES tags(id),
    PRIMARY KEY (post_id, tag_id)
);

-- The primary key covers "tags of this post"; this covers "posts with this tag".
CREATE INDEX post_tags_tag_id_idx ON post_tags (tag_id, post_id);

-- +goose Down

DROP TABLE post_tags;
DROP TABLE posts;
DROP TABLE tags;
ALTER TABLE users DROP COLUMN is_admin;
