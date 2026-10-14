package tags

// Rules for changing the tree (the admin tag page): what a slug and an
// alias may look like, and Check, which looks over a whole tree for names
// that would trip people up. New already refuses what can't work at all
// (loops, unknown tags, a parent of another facet); Check finds what works
// but misleads.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits, matching the database's CHECK constraints (migration 00010).
const (
	MaxSlugLen        = 40
	MaxDisplayLen     = 60
	MaxDescriptionLen = 200
	MaxAliasLen       = 60
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SlugProblem says what's wrong with a slug, or "" if nothing is.
func SlugProblem(slug string) string {
	switch {
	case slug == "":
		return "The slug can't be empty."
	case len(slug) > MaxSlugLen:
		return fmt.Sprintf("The slug can be at most %d characters.", MaxSlugLen)
	case !slugPattern.MatchString(slug):
		return "The slug can only have lowercase letters, digits and single hyphens between them, like “home-networking”."
	}
	return ""
}

// DisplayProblem says what's wrong with a display name, or "".
func DisplayProblem(display string) string {
	switch n := utf8.RuneCountInString(display); {
	case strings.TrimSpace(display) == "":
		return "The display name can't be empty."
	case n > MaxDisplayLen:
		return fmt.Sprintf("The display name can be at most %d characters.", MaxDisplayLen)
	case Normalize(display) == "":
		return "The display name needs at least one letter or digit."
	}
	return ""
}

// DescriptionProblem says what's wrong with a description, or "".
func DescriptionProblem(description string) string {
	if utf8.RuneCountInString(description) > MaxDescriptionLen {
		return fmt.Sprintf("The description can be at most %d characters.", MaxDescriptionLen)
	}
	return ""
}

// AliasProblem says what's wrong with an alias, or "".
//
// The trap it guards against: names are matched after Normalize throws away
// punctuation, so an alias like ".net" really means "net", and anyone typing
// "net" (meaning networking) would land on C#. An alias has to start with a
// letter or digit, so what's written is what it matches.
func AliasProblem(alias string) string {
	alias = strings.TrimSpace(alias)
	first, _ := utf8.DecodeRuneInString(alias)
	switch {
	case alias == "":
		return "The alias can't be empty."
	case utf8.RuneCountInString(alias) > MaxAliasLen:
		return fmt.Sprintf("An alias can be at most %d characters.", MaxAliasLen)
	case Normalize(alias) == "":
		return fmt.Sprintf("The alias “%s” needs at least one letter or digit.", alias)
	case !unicode.IsLetter(first) && !unicode.IsDigit(first):
		return fmt.Sprintf("The alias “%s” starts with punctuation, which matching ignores, so it would really mean “%s”. Spell it out instead (like “dotnet”).",
			alias, Normalize(alias))
	}
	return ""
}

// Slugify suggests a slug for a display name: "Food & drink" becomes
// "food-and-drink", "C++" becomes "c-plus-plus". It's only used when the
// admin leaves the slug empty; the slug shows on the tag's page afterwards,
// and can be changed there.
func Slugify(display string) string {
	display = strings.NewReplacer("&", " and ", "+", " plus ", "#", " sharp ").Replace(strings.ToLower(display))
	var b strings.Builder
	dash := false
	for _, r := range display {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	s := b.String()
	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}
	return s
}

// Check looks over the whole tree for names that work but mislead, and
// returns one sentence per problem (none for a healthy tree):
//
//   - an alias AliasProblem refuses (see there);
//   - an alias that is another tag's own name (slug or display name), which
//     would make typing that tag's real name ambiguous;
//   - two tags whose slugs or display names match once normalized;
//   - a format or level with parents (only topics form a tree).
//
// Two tags SHARING an alias is fine and deliberate ("football"): typing it
// asks which one was meant.
func (t *Tree) Check() []string {
	var problems []string

	// Who owns each normalized slug and display name.
	owners := map[string][]int{}
	for _, id := range t.order {
		tag := t.byID[id]
		for _, name := range []string{tag.Slug, tag.Display} {
			k := Normalize(name)
			if k != "" && !slices.Contains(owners[k], id) {
				owners[k] = append(owners[k], id)
			}
		}
	}
	for _, id := range t.order {
		tag := t.byID[id]
		for _, name := range []string{tag.Slug, tag.Display} {
			for _, other := range owners[Normalize(name)] {
				if other > id { // report each pair once
					problems = append(problems, fmt.Sprintf("%s and %s have the same name once spacing and punctuation are ignored.",
						tag.Slug, t.byID[other].Slug))
				}
			}
		}
	}

	for _, id := range t.order {
		tag := t.byID[id]
		for _, alias := range tag.Aliases {
			if p := AliasProblem(alias); p != "" {
				problems = append(problems, fmt.Sprintf("%s: %s", tag.Slug, p))
				continue
			}
			for _, other := range owners[Normalize(alias)] {
				if other != id {
					problems = append(problems, fmt.Sprintf("%s: the alias “%s” is %s's own name, so typing that would become ambiguous.",
						tag.Slug, alias, t.byID[other].Slug))
				}
			}
		}
		if tag.Facet != Topic && len(tag.Parents) > 0 {
			problems = append(problems, fmt.Sprintf("%s is a %s, and only topics have parents.", tag.Slug, tag.Facet))
		}
	}
	return slices.Compact(problems)
}
