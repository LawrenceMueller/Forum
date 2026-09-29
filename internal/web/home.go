package web

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"
)

type Post struct {
	ID        int
	Title     string
	URL       string // empty for text posts
	Domain    string
	Tags      []string
	Points    int
	Comments  int
	Author    string
	Posted    time.Time
	LastReply time.Time // zero value means "don't show it"
}

// Link is where the title points: the external URL for link posts,
// the post's own page for text posts. Templates can call methods like fields.
func (p Post) Link() string {
	if p.URL != "" {
		return p.URL
	}
	return fmt.Sprintf("/p/%d", p.ID)
}

// Name colors are CSS classes (nc-blue, nc-green, ... in style.css) rather
// than inline style attributes, so the Content Security Policy can forbid
// inline styles entirely.
var nameColors = map[string]bool{
	"blue": true, "green": true, "red": true, "pink": true,
	"orange": true, "yellow": true, "brown": true, "purple": true,
}

const fallbackColorCount = 4 // nc-x0 to nc-x3 in style.css

// colorClass gives a name's little square its color. Names start with a color
// word, so "blue-humming-otter" gets nc-blue. Anything else falls back to
// hashing the name, so it still always gets the same color.
func colorClass(name string) string {
	first, _, _ := strings.Cut(name, "-")
	if nameColors[first] {
		return "nc-" + first
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("nc-x%d", h.Sum32()%fallbackColorCount)
}

func (p Post) AuthorColorClass() string { return colorClass(p.Author) }

type TagCount struct {
	Name  string
	Count int
}

// homePage embeds basePage, so the layout's fields (Title, Tagline...)
// sit right alongside the home page's own data.
type homePage struct {
	basePage
	Popular []Post
	Active  []Post
	Tags    []TagCount
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	data := homePage{
		basePage: newBasePage(r, "front page"),
		Popular:  samplePopular,
		Active:   sampleActive,
		Tags:     sampleTags,
	}

	data.Generated = time.Since(start)
	s.render(w, "home.html", data)
}

// ---- fake data until posts are in the database ----

func at(day, hour, min int) time.Time {
	return time.Date(2026, time.September, day, hour, min, 0, 0, time.Local)
}

var samplePopular = []Post{
	{ID: 1, Title: "I rebuilt my grandfather's shortwave radio and heard Portugal last night",
		Tags: []string{"diy", "show"}, Points: 214, Comments: 61, Author: "blue-humming-otter", Posted: at(26, 6, 12)},
	{ID: 2, Title: "Notes on running Postgres on a $5 server for three years",
		URL: "https://example.org/postgres", Domain: "example.org",
		Tags: []string{"tech"}, Points: 187, Comments: 43, Author: "green-sketching-heron", Posted: at(26, 2, 40)},
	{ID: 3, Title: "Ask: what's a book you reread every winter?",
		Tags: []string{"books", "ask"}, Points: 152, Comments: 128, Author: "purple-reading-owl", Posted: at(25, 21, 5)},
}

var sampleActive = []Post{
	{ID: 6, Title: "Should anonymous posts be editable after an hour?",
		Tags: []string{"meta", "ask"}, Points: 64, Comments: 39, Author: "orange-pondering-walrus",
		Posted: at(25, 12, 48), LastReply: at(26, 14, 52)},
	{ID: 8, Title: "Is it worth learning to solder in my 40s?",
		Tags: []string{"diy", "ask"}, Points: 33, Comments: 22, Author: "red-tinkering-badger",
		Posted: at(25, 7, 2), LastReply: at(26, 14, 47)},
	{ID: 4, Title: "The bread recipe that finally worked after 11 failed attempts",
		Tags: []string{"food", "show"}, Points: 98, Comments: 27, Author: "yellow-baking-hare",
		Posted: at(25, 18, 33), LastReply: at(26, 14, 31)},
	{ID: 5, Title: "A trail map drawn entirely from memory by a retired ranger",
		URL: "https://example.net/trail", Domain: "example.net",
		Tags: []string{"outdoors", "art"}, Points: 76, Comments: 12, Author: "brown-hiking-moose",
		Posted: at(25, 15, 10), LastReply: at(26, 14, 5)},
	{ID: 7, Title: "Tapes I found in a thrift store, digitized",
		Tags: []string{"music", "show"}, Points: 51, Comments: 8, Author: "pink-drumming-axolotl",
		Posted: at(25, 9, 17), LastReply: at(26, 13, 40)},
	{ID: 9, Title: "What are you working on this weekend?",
		Tags: []string{"ask"}, Points: 29, Comments: 17, Author: "green-napping-puffin",
		Posted: at(26, 8, 15), LastReply: at(26, 13, 22)},
}

var sampleTags = []TagCount{
	{"tech", 3102}, {"ask", 2488}, {"books", 1877}, {"show", 1530}, {"diy", 1240},
	{"food", 964}, {"outdoors", 711}, {"music", 655}, {"art", 418}, {"meta", 302},
}
