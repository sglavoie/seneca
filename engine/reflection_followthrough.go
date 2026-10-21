package engine

import "strings"

// A follow-through is the continuity section: the blockquoted snapshot of an
// earlier day's intention, and the narrative response written later.

// FollowThrough is a continuity section: which day's intention it answers, the
// snapshot of that intention, and the narrative response written later.
type FollowThrough struct {
	SourceDate      string
	SourceIntention string
	Response        string
}

// render lays out a follow-through body: blockquoted intention, blank line,
// then the response.
func (f FollowThrough) render() string {
	var b strings.Builder
	if f.SourceIntention != "" {
		for _, line := range strings.Split(f.SourceIntention, "\n") {
			if line == "" {
				b.WriteString(">\n")
				continue
			}
			b.WriteString("> " + line + "\n")
		}
	}
	if f.Response != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(f.Response)
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseFollowThroughBody splits a follow-through body into the blockquoted
// intention snapshot and the narrative response after it.
func parseFollowThroughBody(source, body string) FollowThrough {
	follow := FollowThrough{SourceDate: source}
	if body == "" {
		return follow
	}

	lines := strings.Split(body, "\n")
	cut := 0
	var quoted []string
	for _, line := range lines {
		if !strings.HasPrefix(line, ">") {
			break
		}
		quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(line, ">"), " "))
		cut++
	}

	follow.SourceIntention = strings.TrimSpace(strings.Join(quoted, "\n"))
	follow.Response = strings.TrimSpace(strings.Join(lines[cut:], "\n"))
	return follow
}

func (e ReflectionEntry) FollowThroughSection() (FollowThrough, bool) {
	for _, section := range e.Sections {
		if section.Follow != nil {
			return *section.Follow, true
		}
	}
	return FollowThrough{}, false
}

// newFollowThroughSection builds a canonical follow-through section.
func newFollowThroughSection(f FollowThrough) ReflectionSection {
	section := ReflectionSection{
		Heading:     followThroughHeading,
		headingLine: sectionHeadingPrefix + followThroughHeading,
		Follow:      &f,
	}
	section.setBody(f.render())
	return section
}
