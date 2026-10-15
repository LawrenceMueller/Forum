-- Moderation, slice 1 (design: docs/PROJECT_CONTEXT.md, section 11.2):
-- reports from members, the admin's queue, a reason on every removal, and a
-- log of moderation actions (shown publicly at /modlog, without content).

-- +goose Up

-- Why something was removed, shown in its place ("[removed: spam]").
-- 'held' means hidden automatically by reports, until the admin looks.
-- NULL on things removed before reasons existed.
ALTER TABLE posts ADD COLUMN removal_reason text
    CONSTRAINT post_removal_reason CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held'));
ALTER TABLE comments ADD COLUMN removal_reason text
    CONSTRAINT comment_removal_reason CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held'));

-- One member's report of a post (comment_id NULL) or of a comment on it.
-- The reporter is a keyed fingerprint of their account (auth.VoterKeys
-- Reporter), never the account ID: enough to allow one report per person
-- per item and a daily limit, while nobody, the admin included, can see who
-- reported what. Rows are deleted 30 days after they're handled.
CREATE TABLE reports (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id    bigint      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    comment_id bigint      REFERENCES comments(id) ON DELETE CASCADE,
    reporter   text        NOT NULL,
    reason     text        NOT NULL CONSTRAINT report_reason
                           CHECK (reason IN ('spam', 'harassment', 'personal', 'illegal', 'tags', 'other')),
    note       text        NOT NULL DEFAULT '' CONSTRAINT report_note_length CHECK (char_length(note) <= 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    handled_at timestamptz,
    CONSTRAINT one_report_per_person UNIQUE NULLS NOT DISTINCT (reporter, post_id, comment_id)
);
CREATE INDEX reports_open_idx ON reports (post_id, comment_id) WHERE handled_at IS NULL;
CREATE INDEX reports_reporter_idx ON reports (reporter, created_at);

-- Every moderation action. Public IDs rather than foreign keys, so the log
-- outlives what it describes; no content, no names, no reporters.
CREATE TABLE mod_log (
    id                bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action            text        NOT NULL CONSTRAINT mod_log_action
                                  CHECK (action IN ('remove', 'hold', 'restore', 'dismiss')),
    post_public_id    text        NOT NULL,
    comment_public_id text,       -- NULL: the action was on the post
    reason            text,       -- a removal reason, for removals
    reports           integer     NOT NULL DEFAULT 0, -- open reports the action settled
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mod_log;
DROP TABLE reports;
ALTER TABLE comments DROP COLUMN removal_reason;
ALTER TABLE posts DROP COLUMN removal_reason;
