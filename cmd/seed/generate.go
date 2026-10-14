package main

// The made-up world: who exists, what they posted, who replied to whom, and
// how the votes fell. The shapes matter more than the words:
//
//   - A few people do most of the posting and voting (activity weights
//     follow a power law, like real communities: roughly 80/20).
//   - A few posts get most of the attention (post "quality" is lognormal,
//     so most posts are ordinary and a handful take off).
//   - Recent posts haven't finished collecting votes and comments yet.
//   - Replies cluster: most go to recent comments in a thread.
//
// Even distributions would make every page cost the same and hide the slow
// cases; these shapes produce the hot threads and long tails a real site has.
//
// Everything comes from seeded random generators, so the same seed builds
// the same site every time. Times are relative to when it runs ("3 hours
// ago"), so "Popular today" always has something in it.

import (
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"snowball/internal/auth"
)

// size is how big a site to build.
type size struct {
	users        int
	days         int     // how far back the history goes
	posts        int     // ordinary posts (the fixtures come on top)
	comments     float64 // average comments on a typical post
	postVotes    float64 // average voters on a typical post
	commentVotes float64 // average voters on a typical comment
	megathread   int     // comments on the one giant thread
	maxPerThread int     // cap for ordinary threads
	founderShare float64 // share of accounts older than the history
	deletedShare float64 // share of accounts deleted since
}

var sizes = map[string]size{
	// For looking at: enough to fill every page, small enough to read.
	"small": {users: 120, days: 30, posts: 400, comments: 6, postVotes: 10, commentVotes: 1.2,
		megathread: 600, maxPerThread: 400, founderShare: 0.4, deletedShare: 0.03},
	// For measuring: a busy small forum after a year. Takes a minute or two.
	"large": {users: 20000, days: 365, posts: 100000, comments: 8, postVotes: 16, commentVotes: 1.2,
		megathread: 2500, maxPerThread: 1500, founderShare: 0.1, deletedShare: 0.03},
}

// Unusual posts that every seeded site gets, near the top of "new", so the
// awkward cases are easy to find and look at.
type postKind int

const (
	kindNormal postKind = iota
	kindBrandNew
	kindLongestTitle
	kindUnbroken
	kindUnicode
	kindMarkdown
	kindLongBody
	kindMegathread
	kindDeepChain
	kindControversial
	kindDeletedWithThread
	kindRemoved
	kindDeletedComments
)

// Fixtures, with how long ago each was posted, their topic and format.
var fixtures = []struct {
	kind   postKind
	ago    time.Duration
	tag    string // a top-level topic
	format string // "" for none
}{
	{kindBrandNew, 3 * time.Minute, "relationships", "question"},
	{kindLongestTitle, 2 * time.Hour, "meta", ""},
	{kindUnbroken, 5 * time.Hour, "technology", ""},
	{kindDeletedComments, 6 * time.Hour, "books", "discussion"},
	{kindUnicode, 9 * time.Hour, "food-and-drink", ""},
	{kindDeletedWithThread, 12 * time.Hour, "crafts", "showcase"},
	{kindRemoved, 14 * time.Hour, "music", ""},
	{kindControversial, 20 * time.Hour, "food-and-drink", "discussion"},
	{kindMarkdown, 26 * time.Hour, "meta", "guide"},
	{kindDeepChain, 36 * time.Hour, "outdoors", ""},
	{kindLongBody, 48 * time.Hour, "books", ""},
	{kindMegathread, 72 * time.Hour, "crafts", "discussion"},
}

// Times are kept as Unix seconds, to keep a million comments small in memory.

type user struct {
	id      int64
	name    string
	created int64
	deleted int64 // 0 = active
	admin   bool
	weight  float64 // how active: most are quiet, a few are very busy
}

type post struct {
	id           int64
	publicID     string
	author       int32   // index into world.users
	tags         []int32 // indexes into world.tags: 1 or 2 topics, maybe a format and a level
	tag          string  // the first topic's top-level tag, which picks the vocabulary
	title, url   string
	kind         postKind
	created      int64
	lastActivity int64
	edited       int64 // 0 = never (after the grace period)
	deleted      int64
	removed      int64
	quality      float32
	up, down     int32
	visible      int32 // comment_count: comments neither deleted nor removed
}

type comment struct {
	id       int64
	publicID string
	post     int32 // index into world.posts
	parent   int64 // comment ID, 0 for top-level
	author   int32
	depth    int16
	created  int64
	edited   int64
	deleted  int64
	removed  int64
	quality  float32
	up, down int32
}

type world struct {
	seed     uint64
	size     size
	now      int64
	users    []user
	byJoin   []int32   // user indexes, oldest account first
	joinTime []int64   // byJoin's creation times, for binary search
	joinCum  []float64 // running total of byJoin's weights
	voteCum  []float64 // running total of all users' weights, by index
	posts    []post
	comments []comment
	tags     []tagInfo
	roots    []int32   // top-level topics (indexes into tags)
	rootCum  []float64 // running total of the roots' weights
	formats  []int32
	levels   []int32
}

type tagInfo struct {
	id     int32
	name   string
	facet  string  // topic, format or level
	weight float64 // top-level topics only: how popular
	below  []int32 // top-level topics only: every tag under it, any depth
}

// How popular each top-level topic is. Unlisted ones get 3.
var tagWeights = map[string]float64{
	"technology": 16, "food-and-drink": 10, "games": 10, "music": 8, "books": 8,
	"relationships": 8, "politics": 8, "programming": 7, "sports": 6, "film-and-tv": 6,
	"money-and-work": 6, "health-and-fitness": 6, "animals": 5, "science": 5, "art": 5,
	"outdoors": 5, "crafts": 5, "home-and-garden": 4, "meta": 1,
}

// Salts give each kind of random stream its own sequence for the same ID.
const (
	saltMain uint64 = iota + 1
	saltPostText
	saltCommentText
	saltPostVotes
	saltCommentVotes
	saltPostIDs
	saltCommentIDs
)

func (w *world) rng(salt uint64, id int64) *rand.Rand {
	return rand.New(rand.NewPCG(w.seed^(salt*0x9E3779B97F4A7C15), uint64(id)))
}

func newWorld(seed uint64, sz size, now time.Time, tags []tagInfo) *world {
	w := &world{seed: seed, size: sz, now: now.Unix(), tags: tags}
	r := w.rng(saltMain, 0)

	total := 0.0
	for i, t := range w.tags {
		switch {
		case t.facet == "format":
			w.formats = append(w.formats, int32(i))
		case t.facet == "level":
			w.levels = append(w.levels, int32(i))
		case t.weight > 0: // a top-level topic
			w.roots = append(w.roots, int32(i))
			total += t.weight
			w.rootCum = append(w.rootCum, total)
		}
	}

	w.makeUsers(r)
	w.makePosts(r)
	w.makeComments(r)
	w.markEditsAndDeletions(r)
	w.countVotes()
	return w
}

// ---- users ----

func (w *world) makeUsers(r *rand.Rand) {
	start := w.now - int64(w.size.days)*86400
	taken := map[string]bool{}
	w.users = make([]user, w.size.users)
	for i := range w.users {
		name := auth.RandomName(r.IntN)
		for taken[name] {
			name = auth.RandomName(r.IntN)
		}
		taken[name] = true

		var created int64
		if r.Float64() < w.size.founderShare {
			created = start - int64(r.IntN(180*86400)) // joined before the history starts
		} else {
			created = start + int64(r.Float64()*float64(w.now-start-2*86400))
		}
		// A power law (Pareto): most people are quiet, a few are very busy.
		// The cap keeps any one account from dominating a small site.
		weight := math.Min(math.Pow(1-r.Float64(), -1/1.3), 40)
		w.users[i] = user{id: int64(i + 1), name: name, created: created, weight: weight}
	}

	// The earliest account is the admin, and an active one.
	first := 0
	for i, u := range w.users {
		if u.created < w.users[first].created {
			first = i
		}
	}
	w.users[first].admin = true
	w.users[first].weight = math.Max(w.users[first].weight, 20)

	// Some accounts were deleted recently (after all their activity). Their
	// content stays, shown as "[deleted]".
	for i := range w.users {
		if i != first && r.Float64() < w.size.deletedShare {
			w.users[i].deleted = w.now - int64(r.IntN(6*3600)) - 60
		}
	}

	w.byJoin = make([]int32, len(w.users))
	for i := range w.byJoin {
		w.byJoin[i] = int32(i)
	}
	slices.SortFunc(w.byJoin, func(a, b int32) int { return int(w.users[a].created - w.users[b].created) })
	total := 0.0
	for _, i := range w.byJoin {
		w.joinTime = append(w.joinTime, w.users[i].created)
		total += w.users[i].weight
		w.joinCum = append(w.joinCum, total)
	}
	total = 0
	for _, u := range w.users {
		total += u.weight
		w.voteCum = append(w.voteCum, total)
	}
}

// author picks someone whose account existed at time t, busier people more often.
// (Deleted accounts can't write anything after they were deleted.)
func (w *world) author(r *rand.Rand, t int64) int32 {
	n, _ := slices.BinarySearch(w.joinTime, t)
	if n == 0 {
		n = 1
	}
	var u int32
	for range 20 {
		x := r.Float64() * w.joinCum[n-1]
		i, _ := slices.BinarySearch(w.joinCum[:n], x)
		u = w.byJoin[min(i, n-1)]
		if d := w.users[u].deleted; d == 0 || d > t {
			break
		}
	}
	return u
}

// voter picks anyone, busier people more often.
func (w *world) voter(r *rand.Rand) int32 {
	x := r.Float64() * w.voteCum[len(w.voteCum)-1]
	i, _ := slices.BinarySearch(w.voteCum, x)
	return int32(min(i, len(w.users)-1))
}

// ---- posts ----

func (w *world) makePosts(r *rand.Rand) {
	start := w.now - int64(w.size.days)*86400

	type slot struct {
		t       int64
		fixture int // index into fixtures, or -1
	}
	slots := make([]slot, 0, w.size.posts+len(fixtures))
	for range w.size.posts {
		// Spread evenly over the history. (A daily rhythm would be more
		// realistic, but it depends on the clock time of each run, which
		// would make the same seed build a different site at 9am and 9pm.)
		t := start + int64(r.Float64()*float64(w.now-start-600))
		slots = append(slots, slot{t, -1})
	}
	for i, f := range fixtures {
		slots = append(slots, slot{w.now - int64(f.ago.Seconds()), i})
	}
	// IDs rise with time, as on a real site.
	slices.SortStableFunc(slots, func(a, b slot) int { return int(a.t - b.t) })

	w.posts = make([]post, len(slots))
	for i, s := range slots {
		t := s.t
		p := post{id: int64(i + 1), created: t, lastActivity: t}
		p.publicID = w.publicID(saltPostIDs, p.id)
		p.author = w.author(r, t)
		// Lognormal: most posts ordinary, a few take off.
		p.quality = float32(math.Exp(r.NormFloat64() * 1.1))

		root := w.pickRoot(r)
		format := w.pickFormat(r)
		if s.fixture >= 0 {
			f := fixtures[s.fixture]
			p.kind = f.kind
			root = w.tagNamed(f.tag)
			format = -1
			if f.format != "" {
				format = w.tagNamed(f.format)
			}
		}
		// Usually one topic, somewhere under the root; now and then a
		// second from another part of the tree.
		first := w.pickUnder(r, root)
		p.tags = []int32{first}
		if r.IntN(5) == 0 {
			// Skip a second topic that's the same tag or sits above or below
			// the first (possible through tags with two parents): the site's
			// own rules would keep only one of them.
			other := w.pickRoot(r)
			second := w.pickUnder(r, other)
			related := second == first || second == root || first == other ||
				slices.Contains(w.tags[root].below, second) || slices.Contains(w.tags[other].below, first)
			if !related {
				p.tags = append(p.tags, second)
			}
		}
		if format >= 0 {
			p.tags = append(p.tags, format)
		}
		if len(w.levels) > 0 && r.IntN(10) == 0 {
			p.tags = append(p.tags, w.levels[r.IntN(len(w.levels))])
		}
		p.tag = w.tags[root].name

		wr := newWriter(r, p.tag)
		p.title = wr.title(p.tag)
		if format >= 0 && w.tags[format].name == "showcase" && r.IntN(2) == 0 {
			p.title = "Show: " + p.title
		}
		linkChance := 0.3
		if p.tag == "meta" || (format >= 0 && w.tags[format].name == "question") {
			linkChance = 0.05
		}
		if r.Float64() < linkChance {
			p.url = wr.link()
		}
		applyFixture(&p)
		w.posts[i] = p
	}
}

// pickRoot picks a top-level topic, popular ones more often.
func (w *world) pickRoot(r *rand.Rand) int32 {
	x := r.Float64() * w.rootCum[len(w.rootCum)-1]
	i, _ := slices.BinarySearch(w.rootCum, x)
	return w.roots[min(i, len(w.roots)-1)]
}

// pickUnder picks the root itself, or (more often) a tag somewhere below it.
func (w *world) pickUnder(r *rand.Rand, root int32) int32 {
	below := w.tags[root].below
	if len(below) == 0 || r.IntN(3) == 0 {
		return root
	}
	return below[r.IntN(len(below))]
}

// pickFormat picks a post's format, or -1 for none.
func (w *world) pickFormat(r *rand.Rand) int32 {
	weights := map[string]int{"question": 25, "discussion": 15, "showcase": 8, "guide": 5, "news": 4, "review": 3}
	x := r.IntN(100)
	for _, f := range w.formats { // in a fixed order, so the same seed gives the same site
		x -= weights[w.tags[f].name]
		if x < 0 {
			return f
		}
	}
	return -1 // the remaining 40%: no format
}

func (w *world) tagNamed(name string) int32 {
	for i, t := range w.tags {
		if t.name == name {
			return int32(i)
		}
	}
	return 0
}

func applyFixture(p *post) {
	switch p.kind {
	case kindBrandNew:
		p.title = "Just posted: nothing here yet, no votes, no comments"
		p.url = ""
	case kindLongestTitle:
		p.title = longestTitle()
	case kindUnbroken:
		p.title = "A title with an " + unbrokenWord
		p.url = longLink()
	case kindUnicode:
		p.title = unicodeTitle
		p.url = ""
	case kindMarkdown:
		p.title = "Markdown showcase: every kind of formatting Snowball allows"
		p.url = ""
	case kindLongBody:
		p.title = "A very long post (almost 40,000 characters)"
		p.url = ""
	case kindMegathread:
		p.title = "Megathread: what's everyone working on this week?"
		p.url = ""
		p.quality = 30
	case kindDeepChain:
		p.title = "A reply chain that goes very deep (continue-this-thread pages)"
	case kindControversial:
		p.title = "Pineapple belongs on pizza and I will not be taking questions"
		p.url = ""
		p.quality = 12
	case kindDeletedWithThread:
		p.title = "This post was deleted by its author, but its discussion stays"
		p.url = ""
		p.deleted = p.created + 4*3600
	case kindRemoved:
		p.title = "This post was removed by the admin"
		p.removed = p.created + 3600
	case kindDeletedComments:
		p.title = "A thread with deleted and removed comments in it"
		p.url = ""
	}
}

// publicID turns a sequential ID into a random-looking, never-repeating
// 8-character ID in the same alphabet as the real ones (a-z, 2-7). Shuffling
// 40-bit numbers this way (multiply by an odd number, mix the high bits
// down) is reversible, so two IDs can never collide.
func (w *world) publicID(salt uint64, id int64) string {
	const mask = 1<<40 - 1
	x := (uint64(id) + w.seed*salt*7919) & mask
	for range 3 {
		x = (x * 0x9E3779B97F4A7C15) & mask
		x ^= x >> 17
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = alphabet[x&31]
		x >>= 5
	}
	return string(b[:])
}

// ---- comments ----

// settle is how far along something is in collecting attention: 0 when
// brand new, close to 1 after a few multiples of tau.
func settle(age, tau float64) float64 { return 1 - math.Exp(-age/tau) }

func poisson(r *rand.Rand, mean float64) int {
	if mean <= 0 {
		return 0
	}
	if mean > 30 { // a normal curve is close enough, and fast
		return max(0, int(math.Round(mean+r.NormFloat64()*math.Sqrt(mean))))
	}
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func expSeconds(r *rand.Rand, mean float64) int64 { return int64(r.ExpFloat64() * mean) }

func (w *world) makeComments(r *rand.Rand) {
	for pi := range w.posts {
		p := &w.posts[pi]
		age := float64(w.now - p.created)
		var n int
		switch p.kind {
		case kindBrandNew, kindRemoved:
			n = 0
		case kindMegathread:
			n = w.size.megathread
		case kindDeepChain:
			w.deepChain(r, int32(pi))
			n = 6
		case kindDeletedWithThread, kindDeletedComments, kindControversial:
			n = 25 + r.IntN(15)
		default:
			n = poisson(r, w.size.comments*math.Pow(float64(p.quality), 0.8)*settle(age, 8*3600))
			n = min(n, w.size.maxPerThread)
		}
		w.thread(r, int32(pi), n)
	}
}

// thread adds n comments to a post. Each starts a new top-level comment or
// replies to an earlier one, usually a recent one.
func (w *world) thread(r *rand.Rand, pi int32, n int) {
	p := &w.posts[pi]
	firstOfPost := len(w.comments)
	for range n {
		inThread := w.comments[firstOfPost:]
		c := comment{post: pi, quality: float32(math.Exp(r.NormFloat64() * 0.9))}

		var parent *comment
		if len(inThread) > 0 && r.Float64() < 0.65 {
			if r.Float64() < 0.6 {
				parent = &inThread[len(inThread)-1-r.IntN(min(10, len(inThread)))]
			} else {
				parent = &inThread[r.IntN(len(inThread))]
			}
			if parent.depth >= 15 {
				parent = nil
			}
		}
		if parent == nil {
			spread := 8 * 3600.0
			if p.kind == kindMegathread {
				spread = 20 * 3600
			}
			c.created = p.created + 60 + expSeconds(r, spread)
		} else {
			c.parent = parent.id
			c.depth = parent.depth + 1
			c.created = parent.created + 30 + expSeconds(r, 90*60)
		}
		if c.created >= w.now-30 {
			continue // hasn't happened yet
		}

		if r.Float64() < 0.08 && w.users[p.author].deleted == 0 {
			c.author = p.author // the poster joins in
		} else {
			c.author = w.author(r, c.created)
		}
		c.id = int64(len(w.comments) + 1)
		c.publicID = w.publicID(saltCommentIDs, c.id)
		w.comments = append(w.comments, c)
		p.lastActivity = max(p.lastActivity, c.created)
	}
}

// deepChain is two people replying to each other 14 levels deep, past the
// depth (8) where a thread continues on its own page.
func (w *world) deepChain(r *rand.Rand, pi int32) {
	p := &w.posts[pi]
	a, b := w.author(r, p.created), w.author(r, p.created)
	for tries := 0; a == b && tries < 20; tries++ {
		b = w.author(r, p.created)
	}
	var parent int64
	t := p.created
	for depth := range 14 {
		t += 600 + expSeconds(r, 1800)
		c := comment{post: pi, parent: parent, depth: int16(depth), created: t, quality: 1}
		c.author = a
		if depth%2 == 1 {
			c.author = b
		}
		c.id = int64(len(w.comments) + 1)
		c.publicID = w.publicID(saltCommentIDs, c.id)
		w.comments = append(w.comments, c)
		parent = c.id
		p.lastActivity = max(p.lastActivity, t)
	}
}

// ---- edits and deletions ----

func (w *world) markEditsAndDeletions(r *rand.Rand) {
	for i := range w.posts {
		p := &w.posts[i]
		if p.kind != kindNormal {
			continue
		}
		switch x := r.Float64(); {
		case x < 0.01:
			p.deleted = p.created + 600 + expSeconds(r, 86400)
		case x < 0.02:
			p.removed = p.created + 600 + expSeconds(r, 6*3600)
		case x < 0.07:
			p.edited = p.created + 150 + int64(r.IntN(440)) // after the 2-minute grace, inside the 10-minute window
		}
	}

	hasReplies := make(map[int64]bool)
	for _, c := range w.comments {
		if c.parent != 0 {
			hasReplies[c.parent] = true
		}
	}
	for i := range w.comments {
		c := &w.comments[i]
		x := r.Float64()
		if w.posts[c.post].kind == kindDeletedComments {
			// The showcase thread: deleted comments that still have replies
			// (the thread must stay intact around them), and some removed ones.
			switch {
			case hasReplies[c.id] && x < 0.35:
				c.deleted = c.created + 300 + expSeconds(r, 3600)
			case x < 0.12:
				c.removed = c.created + 300 + expSeconds(r, 3600)
			}
		} else {
			switch {
			case x < 0.02:
				c.deleted = c.created + 300 + expSeconds(r, 3600)
			case x < 0.025:
				c.removed = c.created + 300 + expSeconds(r, 3600)
			case x < 0.065:
				c.edited = c.created + 150 + int64(r.IntN(440)) // after the grace, inside the window
			}
		}
		// Nothing may happen after now.
		for _, t := range []*int64{&c.deleted, &c.removed, &c.edited} {
			if *t > w.now {
				*t = w.now - 1
			}
		}
		if c.deleted == 0 && c.removed == 0 {
			w.posts[c.post].visible++
		}
	}
	for i := range w.posts {
		p := &w.posts[i]
		for _, t := range []*int64{&p.deleted, &p.removed, &p.edited} {
			if *t > w.now {
				*t = w.now - 1
			}
		}
	}
}

// ---- votes ----

// Votes aren't kept in memory (a large site has millions). Each item's votes
// come from its own seeded generator, so they can be produced twice and come
// out identical: once here to count them, and again while writing them.

// postVotes calls emit for each vote on a post.
func (w *world) postVotes(p *post, emit func(voter int32, value int16)) {
	if p.kind == kindBrandNew {
		return
	}
	r := w.rng(saltPostVotes, p.id)
	age := float64(w.now - p.created)
	n := w.size.postVotes * float64(p.quality) * settle(age, 10*3600)
	upChance := 0.62 + 0.3*math.Tanh(math.Log(float64(p.quality)))
	if p.kind == kindControversial {
		upChance = 0.5
	}
	w.castVotes(r, poisson(r, n), p.author, upChance, emit)
}

// commentVotes calls emit for each vote on a comment.
func (w *world) commentVotes(c *comment, emit func(voter int32, value int16)) {
	r := w.rng(saltCommentVotes, c.id)
	p := &w.posts[c.post]
	age := float64(w.now - c.created)
	n := w.size.commentVotes * float64(c.quality) * math.Sqrt(float64(p.quality)) /
		(1 + 0.5*float64(c.depth)) * settle(age, 6*3600)
	upChance := 0.7 + 0.25*math.Tanh(math.Log(float64(c.quality)))
	w.castVotes(r, poisson(r, n), c.author, upChance, emit)
}

// castVotes picks n different voters (never the author) and their votes.
func (w *world) castVotes(r *rand.Rand, n int, author int32, upChance float64, emit func(int32, int16)) {
	n = min(n, len(w.users)*6/10)
	seen := make(map[int32]bool, n)
	for tries := 0; len(seen) < n && tries < n*20; tries++ {
		v := w.voter(r)
		if v == author || seen[v] {
			continue
		}
		seen[v] = true
		value := int16(-1)
		if r.Float64() < upChance {
			value = 1
		}
		emit(v, value)
	}
}

func (w *world) countVotes() {
	for i := range w.posts {
		p := &w.posts[i]
		w.postVotes(p, func(_ int32, v int16) {
			if v > 0 {
				p.up++
			} else {
				p.down++
			}
		})
	}
	for i := range w.comments {
		c := &w.comments[i]
		w.commentVotes(c, func(_ int32, v int16) {
			if v > 0 {
				c.up++
			} else {
				c.down++
			}
		})
	}
}
