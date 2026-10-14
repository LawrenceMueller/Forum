package web

// Request-level tests for tags: tag pages (with everything below them),
// old names and aliases, ambiguous names, the /tags index, the sidebar, the
// tag fields on the edit page, and the topic suggestions while typing.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"snowball/internal/store"
	"snowball/internal/tags"
)

// postTagged creates a post with the given tag slugs.
func (s *site) postTagged(t *testing.T, author *testUser, title string, slugs ...string) store.Post {
	t.Helper()
	var ids []int
	for _, slug := range slugs {
		ids = append(ids, tagID(t, slug))
	}
	pid, err := testStore.CreatePost(ctx, store.NewPost{AuthorID: author.id, Title: title, TagIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := testStore.PostByPublicID(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTagPagesIncludeEverythingBelow(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	s.postTagged(t, me, "My first sourdough loaf", "sourdough")
	s.postTagged(t, me, "Bread in general", "bread")
	s.postTagged(t, me, "A soccer match", "soccer")

	// Food & drink > Baking > Bread > Sourdough: both bread posts show on
	// every level above them.
	for _, path := range []string{"/t/food-and-drink", "/t/baking", "/t/bread"} {
		page := s.get(path, nil)
		expectStatus(t, path, page, http.StatusOK)
		expectContains(t, path, page, "My first sourdough loaf")
		expectContains(t, path, page, "Bread in general")
		expectMissing(t, path, page, "A soccer match")
	}

	// "This tag only" leaves out what's below.
	only := s.get("/t/bread?only=1", nil)
	expectContains(t, "bread only", only, "Bread in general")
	expectMissing(t, "bread only", only, "My first sourdough loaf")

	// The page shows where the tag sits, and what's below it.
	bread := s.get("/t/bread", nil)
	expectContains(t, "path", bread, `<a href="/t/baking">Baking</a>`)
	expectContains(t, "children", bread, `href="/t/sourdough">Sourdough</a>`)
	expectContains(t, "up link", bread, "/t/baking")

	// Sourdough's own page has no children and no scope switch.
	expectMissing(t, "leaf tag", s.get("/t/sourdough", nil), "this tag only")
}

func TestTagNamesAndAliases(t *testing.T) {
	s := newSite(t)

	// Old names and aliases go to the canonical page, keeping the query.
	rec := s.get("/t/food?after=1.2", nil)
	expectStatus(t, "old name", rec, http.StatusMovedPermanently)
	if loc := rec.Header().Get("Location"); loc != "/t/food-and-drink?after=1.2" {
		t.Errorf("redirected to %q", loc)
	}
	if loc := s.get("/t/Sour-Dough", nil).Header().Get("Location"); loc != "/t/sourdough" {
		t.Errorf("a forgiving spelling redirected to %q", loc)
	}

	// A name that means two tags asks which.
	choice := s.get("/t/football", nil)
	expectStatus(t, "ambiguous", choice, http.StatusOK)
	expectContains(t, "ambiguous", choice, `href="/t/soccer"`)
	expectContains(t, "ambiguous", choice, `href="/t/american-football"`)

	expectStatus(t, "unknown", s.get("/t/no-such-tag-at-all", nil), http.StatusNotFound)
}

func TestTagIndexAndCounts(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	s.postTagged(t, me, "Loaf one", "sourdough")
	s.postTagged(t, me, "Loaf two", "sourdough", "question")
	s.srv.refreshStats() // counts are cached; refresh as the minute timer would

	index := s.get("/tags", nil)
	expectStatus(t, "/tags", index, http.StatusOK)
	expectContains(t, "/tags", index, `href="/t/food-and-drink">Food & drink</a> <span>2</span>`)
	expectContains(t, "/tags", index, `href="/t/baking">Baking</a> <span>2</span>`)
	expectContains(t, "/tags", index, `href="/t/question" title="Asking for an answer or help">Question</a> <span>1</span>`)

	// The sidebar lists the busiest top-level topics.
	expectContains(t, "sidebar", s.get("/", nil), `href="/t/food-and-drink">Food & drink</a> <span>2</span>`)
}

func TestEditPageFillsInTags(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")
	p := s.postTagged(t, me, "Tagged", "sourdough", "soccer", "guide", "advanced")

	page := s.get("/p/"+p.PublicID+"/edit", me)
	expectStatus(t, "edit page", page, http.StatusOK)
	body := page.Body.String()
	for _, want := range []string{`value="Soccer, Sourdough"`, `value="guide" selected`, `value="advanced" selected`} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page should contain %s", want)
		}
	}

	// Saving changes the tags, through the same rules as posting.
	rec := s.send("/p/"+p.PublicID+"/edit", form("body", "new text", "topics", "bread",
		"format", "", "level", "", "form_token", s.token(formEditPost)), me)
	expectRedirect(t, "save", rec, "/p/"+p.PublicID)
	after, _, _ := testStore.PostByPublicID(ctx, p.PublicID)
	if strings.Join(after.Tags, ",") != "bread" {
		t.Errorf("tags after editing = %v, want [bread]", after.Tags)
	}
}

func TestTopicSuggestions(t *testing.T) {
	s := newSite(t)
	suggest := func(topics string) *httptest.ResponseRecorder {
		return s.get("/tags/suggest?topics="+url.QueryEscape(topics), nil, "HX-Request", "true")
	}

	// The part being typed gets suggestions, each showing where it sits.
	rec := suggest("sour")
	expectStatus(t, "suggest", rec, http.StatusOK)
	expectContains(t, "suggest", rec, `hx-get="/tags/pick?choose=sourdough"`)
	expectContains(t, "suggest", rec, "Sourdough <span class=\"where\">in Food & drink › Baking › Bread</span>")

	// A shared alias offers every tag it could mean.
	rec = suggest("football")
	expectContains(t, "alias", rec, "choose=soccer")
	expectContains(t, "alias", rec, "choose=american-football")

	// Topics already settled are confirmed, and neither they nor what they
	// already include are offered again.
	rec = suggest("sourdough, ba")
	expectContains(t, "settled", rec, "Topics: <strong>Sourdough</strong>. Also shows under Baking, Bread, Food & drink.")
	expectMissing(t, "settled", rec, "choose=baking\"")
	expectMissing(t, "settled", rec, "choose=sourdough\"")

	// Three settled: nothing more to suggest.
	expectContains(t, "full", suggest("sourdough, soccer, chess, ba"), "That's three topics")

	// Nothing matches: say so.
	expectContains(t, "no match", suggest("sourdough, zzqx"), "No topic matches “zzqx”")

	// Only formats and levels have these names: not offered as topics.
	expectMissing(t, "format", suggest("showcase"), "choose=showcase")

	// Silly lengths are refused before any matching.
	expectStatus(t, "too long", suggest(strings.Repeat("a", 1001)), http.StatusRequestEntityTooLarge)
}

func TestTopicPick(t *testing.T) {
	s := newSite(t)
	pick := func(choose, topics string) *httptest.ResponseRecorder {
		return s.get("/tags/pick?choose="+choose+"&topics="+url.QueryEscape(topics), nil, "HX-Request", "true")
	}

	// The pick replaces what was being typed, keeps the rest, and leaves
	// the box ready (and focused) for the next one.
	rec := pick("sourdough", "soccer, sour")
	expectStatus(t, "pick", rec, http.StatusOK)
	expectContains(t, "pick", rec, `id="topic-field"`)
	expectContains(t, "pick", rec, `value="soccer, Sourdough, "`)
	expectContains(t, "pick", rec, "autofocus")
	expectContains(t, "pick", rec, "Topics: <strong>Soccer</strong>, <strong>Sourdough</strong>")

	// What the box ends up holding is exactly what posting accepts.
	if _, problems := s.srv.tree().Choose(tags.Selection{Topics: "soccer, Sourdough, "}); problems != nil {
		t.Errorf("the picked text doesn't post: %v", problems)
	}

	// Only real topics can be picked.
	expectStatus(t, "format", pick("question", ""), http.StatusBadRequest)
	expectStatus(t, "unknown", pick("no-such-topic", ""), http.StatusBadRequest)
}

func TestTopicFieldWithoutJavaScript(t *testing.T) {
	s := newSite(t)
	me := s.user(t, "blue-humming-otter")

	// The submit page has a plain text box (htmx only adds to it), and the
	// suggestion area starts empty.
	page := s.get("/submit", me)
	expectContains(t, "submit", page, `name="topics" type="text" value=""`)
	expectContains(t, "submit", page, `hx-get="/tags/suggest"`)
	expectMissing(t, "submit", page, "Matching topics")
}
