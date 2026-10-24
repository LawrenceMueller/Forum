package main

// Polls in the seed: about one post in 40 carries one, so lists show the
// [poll] marker and the post pages show both states. Polls on posts from the
// last few days are still open (with their votes' fingerprints, made the way
// the site makes them, so logging in as a seeded account shows its pick);
// older ones are closed, with only their totals, as the site leaves them.
// Comments on a poll by someone who voted carry their pick.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"snowball/internal/auth"
	"snowball/internal/store"
)

const saltPolls uint64 = 0x9011

var pollOptionSets = [][]string{
	{"Yes", "No"},
	{"Agree", "Disagree", "It depends"},
	{"Love it", "It's fine", "Can't stand it", "Never tried it"},
	{"Today", "This week", "Some day", "Never"},
	{"Cats", "Dogs", "Neither"},
	{"Pineapple belongs on pizza", "Pineapple does not belong on pizza"},
	{"A", "B", "C", "D", "E",
		"An option written right up to the limit of eighty characters, to test the layout"},
}

var pollLengths = []time.Duration{3 * time.Hour, 12 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour}

type seedPoll struct {
	post    int32 // index into world.posts
	options []string
	votes   []int32
	closes  int64
	picks   map[int32]int16 // voter (user index) -> option
}

func (sp *seedPoll) open(now int64) bool { return sp.closes > now }

// makePolls picks the posts that carry polls and casts their votes.
func (w *world) makePolls() []*seedPoll {
	var polls []*seedPoll
	for i := range w.posts {
		p := &w.posts[i]
		if p.id%40 != 3 || p.kind != kindNormal || p.deleted != 0 || p.removed != 0 {
			continue
		}
		r := w.rng(saltPolls, p.id)
		sp := &seedPoll{post: int32(i), options: pollOptionSets[r.IntN(len(pollOptionSets))], picks: map[int32]int16{}}
		length := pollLengths[r.IntN(len(pollLengths))]
		if w.now-p.created < 3*24*3600 {
			length = 7 * 24 * time.Hour // recent: still open
		}
		sp.closes = p.created + int64(length.Seconds())
		sp.votes = make([]int32, len(sp.options))

		// Lopsided, so results differ: each option gets a random weight.
		weights := make([]float64, len(sp.options))
		total := 0.0
		for k := range weights {
			weights[k] = 0.2 + r.Float64()
			total += weights[k]
		}
		n := poisson(r, 2*w.size.postVotes*float64(p.quality))
		w.castVotes(r, n, -1, 1, func(v int32, _ int16) {
			if u := w.users[v]; u.created > p.created || (u.deleted != 0 && u.deleted < p.created) {
				// nobody votes before their account exists, or after it's gone
				return
			}
			x, k := r.Float64()*total, 0
			for ; k < len(weights)-1 && x > weights[k]; k++ {
				x -= weights[k]
			}
			sp.picks[v] = int16(k)
			sp.votes[k]++
		})
		polls = append(polls, sp)
	}
	return polls
}

// pollPick is the pick a comment shows: its author's vote in the post's poll.
func pollPick(byPost map[int32]*seedPoll, c *comment) any {
	if sp := byPost[c.post]; sp != nil {
		if k, ok := sp.picks[c.author]; ok {
			return k
		}
	}
	return nil
}

// writePolls stores the polls, their options and totals, and (for open
// polls) the vote fingerprints.
func writePolls(ctx context.Context, tx pgx.Tx, w *world, polls []*seedPoll, keys *auth.VoterKeys) error {
	err := copyRows(ctx, tx, "polls", []string{"post_id", "closes_at"}, len(polls), func(i int) []any {
		return []any{w.posts[polls[i].post].id, at(polls[i].closes)}
	})
	if err != nil {
		return err
	}
	err = copyStream(ctx, tx, "poll_options", []string{"post_id", "position", "label", "votes"}, func(emit func([]any)) {
		for _, sp := range polls {
			for k, label := range sp.options {
				emit([]any{w.posts[sp.post].id, int16(k), label, sp.votes[k]})
			}
		}
	})
	if err != nil {
		return err
	}
	keyCache := map[int32]string{}
	return copyStream(ctx, tx, "poll_votes", []string{"voter", "post_id", "position"}, func(emit func([]any)) {
		for _, sp := range polls {
			if !sp.open(w.now) {
				continue // closed: the site has let go of these
			}
			postID := w.posts[sp.post].id
			for v, k := range sp.picks {
				var voter string
				if w.users[v].deleted != 0 {
					voter = fmt.Sprintf("deleted-%d-%d-%d", w.seed, v, postID) // cut loose, as DeleteAccount does
				} else {
					if keyCache[v] == "" {
						keyCache[v] = keys.PollKey(w.users[v].id)
					}
					voter = store.PollVoter(keyCache[v], postID)
				}
				emit([]any{voter, postID, k})
			}
		}
	})
}
