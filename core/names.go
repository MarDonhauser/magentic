package core

import (
	"fmt"
	"strings"
)

var slugStopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(
		"der die das den dem des ein eine einen einem einer und oder aber auch noch mal bitte dann danach " +
			"in im am an auf aus bei fuer mit von vom nach zu zum zur ueber unter " +
			"soll sollte sollen muss kann ist sind war wird werden es er sie ich du wir man dass wenn wie wo nur so sehr ganz " +
			"the a an and or but to of on at for with from by is are was were be been it this that should must can please do") {
		slugStopwords[w] = true
	}
}

func Slugify(text string) string {
	text = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(text))
	var words []string
	var cur strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	kept := make([]string, 0, 3)
	for _, w := range words {
		if slugStopwords[w] {
			continue
		}
		kept = append(kept, w)
		if len(kept) == 3 {
			break
		}
	}
	if len(kept) == 0 {
		kept = words
		if len(kept) > 3 {
			kept = kept[:3]
		}
	}
	slug := strings.Join(kept, "-")
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-")
	}
	return slug
}

func PickAgentName(s *State, hint string) string {
	base := Slugify(hint)
	if base == "" {
		base = "session"
	}
	if !s.HasAgent(base) && !TmuxHasSession(SessionName(base)) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !s.HasAgent(candidate) && !TmuxHasSession(SessionName(candidate)) {
			return candidate
		}
	}
}
