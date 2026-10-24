-- Polls. Any post can carry one: the post's title is the question, and it
-- gets 2-6 options and a closing time. The options are fixed once posted,
-- like the title and link, so nobody's vote ends up behind different words.
--
-- PRIVACY, the same rules as post_votes (00008), and one more:
--   * voter is a fingerprint made with the vote secret, never the account
--     id, and it's different for every poll (auth.VoterKeys.PollKey and
--     store.PollVoter): even a leaked database can't line up one person's
--     picks across polls.
--   * no timestamps on votes.
--   * once a poll closes, its poll_votes rows are deleted (store.ClosePolls,
--     hourly). Nobody can vote again, so even the fingerprints aren't needed:
--     only the totals in poll_options remain.
--
-- comments.poll_pick is the one public part: a comment on a poll shows the
-- option its author voted for, as a tag beside their name. Voting alone
-- shows nothing; commenting on a poll you voted in shows your side.

-- +goose Up
CREATE TABLE polls (
    post_id   bigint      PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    closes_at timestamptz NOT NULL
);

CREATE TABLE poll_options (
    post_id  bigint   NOT NULL REFERENCES polls(post_id) ON DELETE CASCADE,
    position smallint NOT NULL CONSTRAINT poll_option_position CHECK (position BETWEEN 0 AND 5),
    label    text     NOT NULL CONSTRAINT poll_option_label CHECK (char_length(label) BETWEEN 1 AND 80),
    votes    integer  NOT NULL DEFAULT 0 CONSTRAINT poll_option_votes CHECK (votes >= 0),
    PRIMARY KEY (post_id, position)
);

CREATE TABLE poll_votes (
    voter    text     NOT NULL,
    post_id  bigint   NOT NULL,
    position smallint NOT NULL,
    PRIMARY KEY (post_id, voter),   -- one vote per account per poll
    FOREIGN KEY (post_id, position) REFERENCES poll_options(post_id, position) ON DELETE CASCADE
);

-- Which option a comment's author had voted for (NULL: not a poll, or they
-- hadn't voted). Set when the comment is written, or when they vote later.
ALTER TABLE comments ADD COLUMN poll_pick smallint
    CONSTRAINT comment_poll_pick CHECK (poll_pick BETWEEN 0 AND 5);

-- +goose Down
ALTER TABLE comments DROP COLUMN poll_pick;
DROP TABLE poll_votes;
DROP TABLE poll_options;
DROP TABLE polls;
