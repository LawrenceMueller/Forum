package main

// Mega polls in the seed: one for each of the last three weeks (closed,
// totals only, so /polls has an archive), today's (open, with votes made
// the way the site makes them, so logging in shows your pick and the front
// page has its box), and a week queued ahead for the admin page.
//
// They're extra posts after the generated ones, from "Snowball" (written by
// the admin account), under one top-level topic.

import (
	"context"
	"fmt"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"

	"snowball/internal/auth"
	"snowball/internal/store"
)

const saltMega uint64 = 0x3E6A

var megaQuestions = []struct {
	q       string
	options []string
}{
	{"Is a hot dog a sandwich?", []string{"Yes", "No", "It's its own thing"}},
	{"Pineapple on pizza?", []string{"Belongs", "Does not belong"}},
	{"Best way to eat a Kit Kat?", []string{"Snap off one finger", "Bite straight across", "Nibble the chocolate off first"}},
	{"Toilet paper: over or under?", []string{"Over", "Under", "Whatever's on the roll"}},
	{"Is cereal a soup?", []string{"Yes", "No", "Only with warm milk"}},
	{"Which is the best season?", []string{"Spring", "Summer", "Autumn", "Winter"}},
	{"Would you rather fight one horse-sized duck or a hundred duck-sized horses?", []string{"One horse-sized duck", "A hundred duck-sized horses"}},
	{"GIF: hard G or soft G?", []string{"Hard G", "Soft G", "I just say the letters"}},
	{"How many browser tabs do you have open right now?", []string{"Under 5", "5 to 20", "20 to 100", "I'm afraid to count"}},
	{"Is water wet?", []string{"Yes", "No", "Water makes things wet"}},
	{"Best pizza topping?", []string{"Pepperoni", "Mushroom", "Just cheese", "Anchovies", "Something weird"}},
	{"Morning person or night owl?", []string{"Morning", "Night", "Neither, always tired"}},
	{"Should you put milk in first or cereal in first?", []string{"Cereal first", "Milk first"}},
	{"Tabs or spaces?", []string{"Tabs", "Spaces", "Whatever the formatter says"}},
	{"Is a Pop-Tart a ravioli?", []string{"Yes", "No"}},
	{"Do you wash your legs in the shower?", []string{"Every time", "The water runs down, it counts", "I don't want to talk about it"}},
	{"Best dinosaur?", []string{"T. rex", "Triceratops", "Stegosaurus", "Velociraptor", "Brachiosaurus", "The chicken"}},
	{"Ketchup in the fridge or the cupboard?", []string{"Fridge", "Cupboard"}},
	{"Would you go to the Moon if offered a seat tomorrow?", []string{"Instantly", "After some training", "Absolutely not"}},
	{"Is it pronounced 'data' like 'day-ta' or 'dah-ta'?", []string{"Day-ta", "Dah-ta"}},
	{"Best sound in the world?", []string{"Rain on a roof", "A cat purring", "Bacon frying", "Silence"}},
	{"Do socks go with sandals?", []string{"Yes", "Never", "Only ironically"}},
	// queued ahead, for the admin page
	{"What's the best kind of potato?", []string{"Fries", "Mashed", "Roast", "Hash browns", "Chips (the crisp kind)"}},
	{"Should Snowball get a dark mode?", []string{"Yes please", "No, the light is the charm"}},
	{"Is a taco a sandwich?", []string{"Yes", "No", "Stop it"}},
	{"Paper books or e-readers?", []string{"Paper", "E-reader", "Audiobooks"}},
	{"Cake or pie?", []string{"Cake", "Pie"}},
	{"Best time to go to the grocery store?", []string{"Early morning", "Lunchtime", "Late at night"}},
	{"Do you name your houseplants?", []string{"All of them", "Some", "They're just plants"}},
}

// megaPast is how many closed mega polls the archive starts with.
const megaPast = 21

// writeMegaPolls adds the mega poll posts (with their tags) and the queue.
// It runs after the posts and post_tags are written and before tag_posts
// is filled, so the new posts are listed under their tag too.
func writeMegaPolls(ctx context.Context, tx pgx.Tx, w *world, keys *auth.VoterKeys) error {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return err
	}
	admin := int32(-1)
	for i := range w.users {
		if w.users[i].admin {
			admin = int32(i)
		}
	}
	if admin < 0 {
		return nil
	}
	topic := w.tags[w.roots[0]]
	now := time.Unix(w.now, 0).In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)

	for k, mq := range megaQuestions {
		dayStart := today.AddDate(0, 0, k-megaPast) // k = megaPast is today
		day := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, time.UTC)
		var postID any
		if k <= megaPast {
			id := int64(len(w.posts) + 1 + k)
			postID = id
			created := dayStart.Add(time.Minute)
			closes := dayStart.AddDate(0, 0, 1)
			if _, err := tx.Exec(ctx, `
				INSERT INTO posts (id, public_id, author_id, title, body, created_at, last_activity_at, content_hash, by_site)
				OVERRIDING SYSTEM VALUE -- the id follows on from the generated posts
				VALUES ($1, $2, $3, $4, '', $5, $5, $6, true)`,
				id, w.publicID(saltPostIDs, id), w.users[admin].id, mq.q, created, store.ContentHash(mq.q, "", "")); err != nil {
				return fmt.Errorf("mega poll post: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO post_tags (post_id, tag_id) VALUES ($1, $2)`, id, topic.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO polls (post_id, closes_at, mega) VALUES ($1, $2, true)`, id, closes); err != nil {
				return err
			}

			// Votes: lots, lopsided. Today's keep their fingerprints.
			r := w.rng(saltMega, id)
			votes := make([]int, len(mq.options))
			weights := make([]float64, len(mq.options))
			total := 0.0
			for i := range weights {
				weights[i] = 0.2 + r.Float64()
				total += weights[i]
			}
			open := closes.After(time.Unix(w.now, 0))
			n := poisson(r, 6*w.size.postVotes)
			if open {
				n = n * int(time.Unix(w.now, 0).Sub(dayStart).Hours()+1) / 24 // the day's not over yet
			}
			var picks [][2]int64 // user index, option
			w.castVotes(r, n, -1, 1, func(v int32, _ int16) {
				u := w.users[v]
				if u.created > created.Unix() || u.deleted != 0 {
					return
				}
				x, i := r.Float64()*total, 0
				for ; i < len(weights)-1 && x > weights[i]; i++ {
					x -= weights[i]
				}
				votes[i]++
				picks = append(picks, [2]int64{int64(v), int64(i)})
			})
			for i, label := range mq.options {
				if _, err := tx.Exec(ctx, `INSERT INTO poll_options (post_id, position, label, votes) VALUES ($1, $2, $3, $4)`,
					id, i, label, votes[i]); err != nil {
					return err
				}
			}
			if open {
				err := copyRows(ctx, tx, "poll_votes", []string{"voter", "post_id", "position"}, len(picks), func(i int) []any {
					u := w.users[picks[i][0]]
					return []any{store.PollVoter(keys.PollKey(u.id), id), id, int16(picks[i][1])}
				})
				if err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO mega_polls (day, question, options, topics, created_by, post_id)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			day, mq.q, mq.options, topic.name, w.users[admin].id, postID); err != nil {
			return fmt.Errorf("mega poll queue: %w", err)
		}
	}
	return nil
}
