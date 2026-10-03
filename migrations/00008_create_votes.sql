-- +goose Up

-- Posts count up and down votes separately too (comments already do),
-- so ranking can use the ratio, not just the difference.
ALTER TABLE posts
    ADD COLUMN upvotes   integer NOT NULL DEFAULT 0,
    ADD COLUMN downvotes integer NOT NULL DEFAULT 0;

-- score is kept for fast sorting; this guarantees it can never drift.
ALTER TABLE posts ADD CONSTRAINT score_matches_votes CHECK (score = upvotes - downvotes);

-- PRIVACY. These two tables map accounts to their opinions, so:
--   * voter is HMAC(secret, account id), never the account id itself. The
--     secret lives outside the database, so a leaked dump or backup can't
--     tell whose votes are whose.
--   * there are no timestamps: when someone voted would reveal activity
--     patterns, and nothing needs it.
--   * when an account is deleted, its voter values are replaced with random
--     ones, so its votes still count but can never be traced back.
-- Nothing in Snowball ever displays or logs individual votes.

CREATE TABLE post_votes (
    voter    text     NOT NULL,
    post_id  bigint   NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    value    smallint NOT NULL CONSTRAINT post_vote_value CHECK (value IN (-1, 1)),
    PRIMARY KEY (voter, post_id)   -- one vote per account per post
);

CREATE TABLE comment_votes (
    voter       text     NOT NULL,
    comment_id  bigint   NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    value       smallint NOT NULL CONSTRAINT comment_vote_value CHECK (value IN (-1, 1)),
    PRIMARY KEY (voter, comment_id)
);

-- +goose Down

DROP TABLE comment_votes;
DROP TABLE post_votes;
ALTER TABLE posts DROP CONSTRAINT score_matches_votes;
ALTER TABLE posts DROP COLUMN downvotes, DROP COLUMN upvotes;
