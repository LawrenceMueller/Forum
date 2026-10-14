-- tag_posts: every post listed under every tag it shows up under, with the
-- post's time, so a tag page is one quick walk down an index.
--
-- Why: listing "newest posts under Question" used to start from post_tags
-- and tag_closure, gather EVERY matching post (25,000 on the large seed),
-- then sort them all to show 30: about 95 ms, growing with the site. Here,
-- the index on (tag_id, created_at, post_id) is already in page order, so
-- Postgres reads the first 31 entries and stops: about 1.5 ms, however big
-- the site gets.
--
-- It's derived data: rebuilt from post_tags and tag_closure for a post
-- whenever its tags change, and for every post under a tag whenever the tree
-- changes around it (store/tagposts.go). Removed and deleted posts stay in
-- it; lists skip them as they always have.

-- +goose Up
CREATE TABLE tag_posts (
    tag_id     integer     NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    post_id    bigint      NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL, -- the post's, copied (it never changes)
    direct     boolean     NOT NULL, -- the post carries exactly this tag (not just one below it)
    PRIMARY KEY (tag_id, post_id)
);

INSERT INTO tag_posts (tag_id, post_id, created_at, direct)
SELECT c.ancestor_id, p.id, p.created_at, bool_or(c.depth = 0)
FROM post_tags pt
JOIN tag_closure c ON c.descendant_id = pt.tag_id
JOIN posts p ON p.id = pt.post_id
GROUP BY c.ancestor_id, p.id, p.created_at;

-- Tag pages, newest first; and the same for "this tag only".
CREATE INDEX tag_posts_newest_idx ON tag_posts (tag_id, created_at DESC, post_id DESC);
CREATE INDEX tag_posts_direct_newest_idx ON tag_posts (tag_id, created_at DESC, post_id DESC) WHERE direct;
-- Rebuilding one post's rows finds them by post.
CREATE INDEX tag_posts_post_id_idx ON tag_posts (post_id);

-- +goose Down
DROP TABLE tag_posts;
