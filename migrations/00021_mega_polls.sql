-- Mega polls: the daily question. The admin writes them ahead into a queue,
-- one per day (mega_polls). On its day (Pacific time) the site publishes it
-- as an ordinary post with a poll, shown as from "Snowball" (posts.by_site)
-- rather than the admin's own name, and marked as a mega poll (polls.mega:
-- results stay hidden until it closes at midnight). After that it's a post
-- like any other; the queue row keeps a link to it for the /polls archive.
--
-- The queue keeps the topics as typed (topics, format, level), and they're
-- matched to tags when the poll is published, so renaming or merging a tag
-- in between doesn't break it. If they no longer match anything, the poll
-- isn't published and publish_problem says why, on the admin page.

-- +goose Up
ALTER TABLE posts ADD COLUMN by_site boolean NOT NULL DEFAULT false;
ALTER TABLE polls ADD COLUMN mega boolean NOT NULL DEFAULT false;

CREATE TABLE mega_polls (
    day             date    PRIMARY KEY,
    question        text    NOT NULL CONSTRAINT mega_question_length CHECK (char_length(question) BETWEEN 1 AND 200),
    body            text    NOT NULL DEFAULT '' CONSTRAINT mega_body_length CHECK (char_length(body) <= 2000),
    options         text[]  NOT NULL CONSTRAINT mega_option_count CHECK (cardinality(options) BETWEEN 2 AND 6),
    topics          text    NOT NULL,
    format          text    NOT NULL DEFAULT '',
    level           text    NOT NULL DEFAULT '',
    created_by      bigint  NOT NULL REFERENCES users(id),
    post_id         bigint  UNIQUE REFERENCES posts(id) ON DELETE SET NULL,
    publish_problem text    NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE mega_polls;
ALTER TABLE polls DROP COLUMN mega;
ALTER TABLE posts DROP COLUMN by_site;
