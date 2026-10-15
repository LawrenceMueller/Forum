-- Moderation, slice 4: the content checks. A blocklist of sites links may
-- not point to, edited by the admin; and an index so duplicate comments can
-- be found by their content hash (posts already have one, from 00006).

-- +goose Up
CREATE TABLE blocked_domains (
    domain     text        PRIMARY KEY CONSTRAINT blocked_domain_format
                           CHECK (domain ~ '^[a-z0-9-]+(\.[a-z0-9-]+)+$' AND char_length(domain) <= 253),
    note       text        NOT NULL DEFAULT '' CONSTRAINT blocked_domain_note_length CHECK (char_length(note) <= 200),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX comments_content_hash_idx ON comments (content_hash, created_at);

-- +goose Down
DROP INDEX comments_content_hash_idx;
DROP TABLE blocked_domains;
