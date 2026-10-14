package main

// Fake writing: titles, post bodies, and comments, assembled from templates
// and per-tag word lists. It only has to look plausible at a glance, so the
// site's layout gets real-looking text of realistic lengths. Everything is
// invented here: no real people's words, and links only point to reserved
// example domains (.example, example.com/org/net), which can never belong
// to anyone.

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// A topic is the vocabulary for one tag.
type topic struct {
	nouns []string    // things: "sourdough starter"
	adjs  []string    // "cast-iron", "secondhand"
	verbs [][2]string // base and past: {"bake", "baked"}
	sites []string    // where link posts point: "recipes.example"
}

var topics = map[string]topic{
	"art": {
		nouns: []string{"watercolor", "sketchbook", "charcoal study", "oil painting", "linocut print", "gouache palette", "portrait", "life drawing", "zine", "mural", "color wheel", "brush set"},
		adjs:  []string{"loose", "tiny", "unfinished", "abstract", "monochrome", "messy", "large-format", "pocket-sized"},
		verbs: [][2]string{{"paint", "painted"}, {"sketch", "sketched"}, {"frame", "framed"}, {"print", "printed"}, {"finish", "finished"}},
		sites: []string{"gallery.example", "artnotes.example", "example.org/art"},
	},
	"ask": {
		nouns: []string{"morning routine", "first job", "budget spreadsheet", "commute", "long-distance move", "apartment search", "career change", "sleep schedule", "group chat", "side project", "landlord", "job interview"},
		adjs:  []string{"awkward", "unexpected", "honest", "practical", "low-effort", "stressful", "small", "weird"},
		verbs: [][2]string{{"handle", "handled"}, {"plan", "planned"}, {"quit", "quit"}, {"fix", "fixed"}, {"start", "started"}},
		sites: []string{"advice.example", "example.com/answers"},
	},
	"books": {
		nouns: []string{"paperback", "library card", "reading list", "short story collection", "translated novel", "used bookstore", "book club", "audiobook", "series", "first edition", "poetry anthology", "doorstop fantasy novel"},
		adjs:  []string{"slow-burning", "forgotten", "dog-eared", "overhyped", "underrated", "500-page", "secondhand", "out-of-print"},
		verbs: [][2]string{{"read", "read"}, {"finish", "finished"}, {"reread", "reread"}, {"abandon", "abandoned"}, {"recommend", "recommended"}},
		sites: []string{"bookshelf.example", "reviews.example", "example.net/books"},
	},
	"crafts": {
		nouns: []string{"workbench", "bookshelf", "raised garden bed", "drill press", "leaky faucet", "deck stain", "router table", "cabinet hinge", "pegboard wall", "light switch", "bike rack", "spice rack"},
		adjs:  []string{"crooked", "reclaimed", "sturdy", "budget", "overbuilt", "wobbly", "hand-cut", "salvaged"},
		verbs: [][2]string{{"build", "built"}, {"fix", "fixed"}, {"sand", "sanded"}, {"rewire", "rewired"}, {"refinish", "refinished"}},
		sites: []string{"workshop.example", "example.com/howto"},
	},
	"food-and-drink": {
		nouns: []string{"sourdough starter", "cast-iron pan", "weeknight curry", "lentil soup", "rice cooker", "chili recipe", "bread oven", "dumpling fold", "spice drawer", "tomato sauce", "pie crust", "fermented hot sauce"},
		adjs:  []string{"crispy", "overproofed", "smoky", "two-ingredient", "slow-cooked", "budget", "spicy", "burnt"},
		verbs: [][2]string{{"cook", "cooked"}, {"bake", "baked"}, {"ferment", "fermented"}, {"season", "seasoned"}, {"braise", "braised"}},
		sites: []string{"recipes.example", "kitchen.example", "example.org/recipes"},
	},
	"meta": {
		nouns: []string{"tag list", "front page", "comment sorting", "edit window", "vote limit", "dark mode", "search", "footer", "sign-up page", "reply box", "new tag", "rules page"},
		adjs:  []string{"small", "minor", "confusing", "welcome", "missing", "slow"},
		verbs: [][2]string{{"change", "changed"}, {"add", "added"}, {"fix", "fixed"}, {"remove", "removed"}, {"clarify", "clarified"}},
		sites: []string{"example.com/meta"},
	},
	"music": {
		nouns: []string{"bass line", "record player", "drum machine", "mixtape", "live album", "chord progression", "secondhand guitar", "synth patch", "vinyl crate", "practice amp", "film score", "B-side"},
		adjs:  []string{"lo-fi", "overplayed", "obscure", "nine-minute", "instrumental", "scratchy", "warm", "detuned"},
		verbs: [][2]string{{"record", "recorded"}, {"learn", "learned"}, {"mix", "mixed"}, {"tune", "tuned"}, {"cover", "covered"}},
		sites: []string{"liner-notes.example", "example.net/music"},
	},
	"outdoors": {
		nouns: []string{"day hike", "tent", "trail map", "rain shell", "campsite", "water filter", "summit", "canoe trip", "trail runner", "bear canister", "sleeping pad", "ridge walk"},
		adjs:  []string{"muddy", "ultralight", "windy", "overnight", "secondhand", "steep", "foggy", "quiet"},
		verbs: [][2]string{{"hike", "hiked"}, {"pack", "packed"}, {"climb", "climbed"}, {"paddle", "paddled"}, {"camp", "camped"}},
		sites: []string{"trails.example", "example.org/outdoors"},
	},
	"show": {
		nouns: []string{"pixel art game", "knitted sweater", "bird feeder", "terminal theme", "ceramic mug", "photo series", "keyboard build", "tiny web page", "board game prototype", "bonsai", "quilt", "map of my neighborhood"},
		adjs:  []string{"first", "finished", "half-finished", "tiny", "ridiculous", "handmade", "overengineered", "cozy"},
		verbs: [][2]string{{"make", "made"}, {"build", "built"}, {"finish", "finished"}, {"design", "designed"}, {"ship", "shipped"}},
		sites: []string{"portfolio.example", "example.com/projects"},
	},
	"technology": {
		nouns: []string{"home server", "mechanical keyboard", "Postgres index", "old laptop", "Raspberry Pi", "password manager", "text editor", "backup script", "Go side project", "router firmware", "e-reader", "self-hosted wiki"},
		adjs:  []string{"refurbished", "open-source", "ten-year-old", "minimal", "self-hosted", "overkill", "fanless", "bargain-bin"},
		verbs: [][2]string{{"set up", "set up"}, {"debug", "debugged"}, {"rewrite", "rewrote"}, {"upgrade", "upgraded"}, {"migrate", "migrated"}},
		sites: []string{"devblog.example", "changelog.example", "example.net/tech"},
	},
}

// Words for placeholders not tied to a tag.
var (
	whens     = []string{"week", "month", "weekend", "spring", "winter", "summer", "year"}
	units     = []string{"hours", "days", "weeks", "months", "evenings", "tries"}
	outcomes  = []string{"better than expected", "a total mess", "surprisingly good", "fine, I guess", "way harder than it looked", "exactly as advertised", "mostly a success"}
	relatives = []string{"partner", "roommate", "sister", "brother", "neighbor", "coworker", "dad", "mom"}
	praises   = []string{"great", "really solid", "impressive", "lovely", "clean", "genuinely useful"}
)

var titleTemplates = []string{
	"What's your favorite {noun}?",
	"I finally {past} my first {noun}",
	"Is a {adj} {noun} worth it?",
	"{Adj} {noun} recommendations?",
	"Anyone else think the {noun} is overrated?",
	"Spent {num} {unit} on a {noun}. Was it worth it?",
	"Help: my {noun} keeps going wrong",
	"TIL you can {verb} a {noun} in half the time",
	"The {adj} {noun} I {past} this weekend",
	"Beginner question about the {noun}",
	"Why does nobody talk about the {noun}?",
	"Unpopular opinion: a {adj} {noun} beats a {noun2}",
	"How long did it take you to {verb} your first {noun}?",
	"What would you change about your {noun}?",
	"A {adj} {noun}, {num} {unit} later",
	"Is it normal for a {noun} to be this {adj}?",
	"Looking for advice before I {verb} a {noun}",
	"My {relative} says I should {verb} the {noun}. Thoughts?",
}

var titlePrefix = map[string][]string{
	"meta": {"Suggestion: ", "Question: ", "Bug: "},
}

var bodySentences = []string{
	"I {past} a {noun} last {when} and it turned out {outcome}.",
	"Most guides say to {verb} the {noun} first, but that never worked for me.",
	"The {adj} {noun} is honestly the part I keep coming back to.",
	"My {relative} thinks I'm wasting my time, but I disagree.",
	"If you've never tried a {adj} {noun}, it's worth a weekend.",
	"Took about {num} {unit}, mostly because I kept second-guessing myself.",
	"Happy to answer questions if anyone is thinking about trying this.",
	"Is there something obvious I'm missing?",
	"Money was tight, so I went with a {adj} {noun} instead of a {noun2}.",
	"The trick, as far as I can tell, is patience.",
	"Not sure this is the right tag, but here goes.",
	"Curious what everyone else does.",
	"Every time I {verb} the {noun} it comes out a little different.",
	"I read somewhere that a {noun2} helps, and it did, a bit.",
	"For context, I've only been at this for a few {unit}.",
	"The second attempt went {outcome}, which I'll take.",
	"Nobody warned me how much the {noun} would matter.",
	"I'm sure I'll laugh at this in a year, but right now I'm proud of it.",
}

var shortComments = []string{
	"This is great.", "Same here.", "Thanks for sharing!", "Saving this.", "Underrated post.",
	"Came here to say this.", "Ha, fair enough.", "Seconded.", "This.", "Good question, following.",
	"Love it.", "Oh no.", "Exactly my experience.", "Wait, really?", "Bookmarked.",
}

var commentSentences = []string{
	"I {past} a {noun} once and never looked back.",
	"Have you tried a {adj} {noun}? It made a big difference for me.",
	"Respectfully, I think the {noun} matters less than people say.",
	"That's a {praise} result for a first try.",
	"I'd skip the {noun} and go straight to a {noun2}.",
	"Where did you get the {noun}?",
	"My experience was the opposite: {outcome}.",
	"Not to be that person, but the {noun} isn't really the issue here.",
	"How long did the {noun} take you?",
	"My {relative} did exactly this last {when}.",
	"Honestly the {adj} version is the best version.",
	"Give it a few {unit} and it gets easier.",
	"I tried this and it went {outcome}.",
	"Agree with most of this, except the part about the {noun}.",
}

// writer fills templates for one topic, using one random generator.
type writer struct {
	r *rand.Rand
	t topic
}

func newWriter(r *rand.Rand, tag string) writer {
	t, ok := topics[tag]
	if !ok {
		t = topics["ask"]
	}
	return writer{r: r, t: t}
}

func (w writer) pick(list []string) string { return list[w.r.IntN(len(list))] }

// fill replaces the {placeholders} in a template.
func (w writer) fill(tmpl string) string {
	noun := w.pick(w.t.nouns)
	noun2 := w.pick(w.t.nouns)
	for noun2 == noun && len(w.t.nouns) > 1 {
		noun2 = w.pick(w.t.nouns)
	}
	verb := w.t.verbs[w.r.IntN(len(w.t.verbs))]
	adj := w.pick(w.t.adjs)
	return strings.NewReplacer(
		"{noun}", noun, "{noun2}", noun2,
		"{adj}", adj, "{Adj}", strings.ToUpper(adj[:1])+adj[1:],
		"{verb}", verb[0], "{past}", verb[1],
		"{num}", fmt.Sprint(2+w.r.IntN(30)),
		"{unit}", w.pick(units), "{when}", w.pick(whens),
		"{outcome}", w.pick(outcomes), "{relative}", w.pick(relatives),
		"{praise}", w.pick(praises),
	).Replace(tmpl)
}

func (w writer) title(tag string) string {
	prefix := ""
	if p, ok := titlePrefix[tag]; ok {
		prefix = w.pick(p)
	}
	return prefix + w.fill(w.pick(titleTemplates))
}

// link is an address on a reserved example domain.
func (w writer) link() string {
	slug := strings.ReplaceAll(w.pick(w.t.nouns), " ", "-")
	return fmt.Sprintf("https://%s/%s-%d", w.pick(w.t.sites), strings.ToLower(slug), 100+w.r.IntN(9900))
}

// paragraph is 1 to max sentences from the given list.
func (w writer) paragraph(sentences []string, max int) string {
	n := 1 + w.r.IntN(max)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = w.fill(w.pick(sentences))
	}
	return strings.Join(parts, " ")
}

// decorate adds some Markdown now and then, so rendering gets exercised.
func (w writer) decorate(p string) string {
	switch w.r.IntN(14) {
	case 0:
		return p + "\n\n- " + w.pick(w.t.nouns) + "\n- " + w.pick(w.t.nouns) + "\n- " + w.pick(w.t.nouns)
	case 1:
		return "**" + p + "**"
	case 2:
		return p + " More here: [" + w.pick(w.t.nouns) + "](" + w.link() + ")"
	case 3:
		return "> " + w.fill(w.pick(commentSentences)) + "\n\n" + p
	case 4:
		return p + " (*" + w.pick(outcomes) + "*)"
	case 5:
		return p + " ~~" + w.pick(w.t.nouns) + "~~ " + w.pick(w.t.nouns) + "."
	}
	return p
}

// postBody is empty for many link posts, a paragraph or several otherwise.
func (w writer) postBody(isLink bool) string {
	if isLink && w.r.IntN(3) > 0 {
		return ""
	}
	n := 1 + w.r.IntN(4)
	paras := make([]string, n)
	for i := range paras {
		paras[i] = w.decorate(w.paragraph(bodySentences, 4))
	}
	return strings.Join(paras, "\n\n")
}

// comment is mostly short, sometimes long: like real threads.
func (w writer) comment() string {
	switch r := w.r.IntN(20); {
	case r < 6:
		return w.pick(shortComments)
	case r < 17:
		return w.decorate(w.paragraph(commentSentences, 3))
	default:
		n := 2 + w.r.IntN(3)
		paras := make([]string, n)
		for i := range paras {
			paras[i] = w.paragraph(append(commentSentences, bodySentences...), 4)
		}
		return strings.Join(paras, "\n\n")
	}
}

// ---- deliberately awkward content, for checking the layout ----

// longestTitle is exactly 200 characters, the most the database allows.
func longestTitle() string {
	t := "This title is as long as Snowball allows, to check that the longest possible title still wraps neatly on a phone, in the post lists, on the post page, and in the history page without breaking"
	return (t + strings.Repeat(" ok", 100))[:200]
}

// unbrokenWord has no spaces anywhere, so only CSS can stop it overflowing.
const unbrokenWord = "Supercalifragilisticexpialidocious-and-then-some-with-no-spaces-anywhere-at-all-to-see-whether-it-overflows"

func longLink() string {
	return "https://example.com/" + strings.Repeat("very-long-path-segment/", 12) + "?utm_source=nowhere&ref=" + strings.Repeat("x", 60)
}

const unicodeTitle = "Café crème brûlée, 東京 ramen, and Ünïcödé — does everything render? 🎉"

const unicodeBody = "Accents: naïve façade, jalapeño, smörgåsbord.\n\n" +
	"Other scripts: 日本語のテキスト, 한국어, Ελληνικά, русский текст, עברית, العربية.\n\n" +
	"Emoji: 🐢🦦🐇 and a family 👨‍👩‍👧 (one character built from several).\n\n" +
	"Zero-width joiners and combining marks: é (an e plus an accent)."

const markdownShowcase = "Everything Snowball's Markdown allows, in one place.\n\n" +
	"**Bold**, *italic*, ***both***, ~~strikethrough~~, and `inline code`.\n\n" +
	"A link: [example](https://example.org/showcase), and a bare one: https://example.com/bare\n\n" +
	"- A bullet\n- Another bullet\n  - A nested bullet\n\n" +
	"1. First\n2. Second\n3. Third\n\n" +
	"> A quotation, which can run on for a while so that it wraps onto a second line on smaller screens.\n\n" +
	"```\nfunc main() {\n\tfmt.Println(\"a code block keeps its spacing\")\n}\n```\n\n" +
	"---\n\n" +
	"# A heading (shown as plain text, since headings aren't allowed)\n\n" +
	"<script>alert('and raw HTML is shown as text, never run')</script>\n\n" +
	"![an image](https://example.com/pixel.png) (external images aren't allowed either)"

// longBody is just under 40,000 characters, the most a post may hold.
func (w writer) longBody() string {
	var b strings.Builder
	b.WriteString("A very long post, to see how the page copes.\n\n")
	for b.Len() < 39000 {
		b.WriteString(w.paragraph(bodySentences, 6))
		b.WriteString("\n\n")
	}
	runes := []rune(b.String())
	if len(runes) > 39900 {
		runes = runes[:39900]
	}
	return strings.TrimSpace(string(runes))
}
