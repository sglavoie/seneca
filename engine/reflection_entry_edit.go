package engine

// Entry edits. Every mutator clones the section slice before touching it,
// because entries are copied by value all over the command layer.

// applyAnswers rewrites the configured question sections in place, appends
// newly configured questions, and leaves every other section — unknown
// headings, follow-through, historical questions — in its original position.
func (e *ReflectionEntry) ApplyAnswers(questions, answers []string, anchorQuestionIndex int) {
	// Entries are copied by value all over the command layer; clone the
	// section slice so editing one copy cannot reach through to another.
	e.Sections = append([]ReflectionSection(nil), e.Sections...)

	index := make(map[string]int, len(questions))
	for i, question := range questions {
		index[question] = i
	}
	matched := make([]bool, len(questions))

	answerAt := func(i int) string {
		if i < len(answers) {
			return answers[i]
		}
		return ""
	}

	for i := range e.Sections {
		section := &e.Sections[i]
		qi, ok := index[section.Heading]
		if !ok {
			continue
		}
		section.setBody(answerAt(qi))
		matched[qi] = true
	}

	for qi, question := range questions {
		if matched[qi] {
			continue
		}
		section := ReflectionSection{
			Heading:     question,
			headingLine: sectionHeadingPrefix + question,
		}
		section.setBody(answerAt(qi))
		e.Sections = append(e.Sections, section)
	}

	e.applyAnchorRole(questions, anchorQuestionIndex)
}

// applyAnchorRole moves the anchor role onto the currently configured anchor
// question. Index 0 means the anchor is disabled and clears the role.
func (e *ReflectionEntry) applyAnchorRole(questions []string, anchorQuestionIndex int) {
	target := ""
	if idx := anchorQuestionIndex - 1; idx >= 0 && idx < len(questions) {
		target = questions[idx]
	}
	for i := range e.Sections {
		section := &e.Sections[i]
		switch {
		case target != "" && section.Heading == target:
			section.role = roleAnchor
		case section.role == roleAnchor:
			section.role = ""
		}
	}
}

// setFollowThrough writes f into the entry's follow-through section, replacing
// the existing one in place so its position and any foreign markers on it
// survive. An entry without one gets a new section appended after everything
// the author already wrote.
func (e *ReflectionEntry) SetFollowThrough(f FollowThrough) {
	// Entries are copied by value across the command layer; clone before
	// mutating so one copy cannot reach through to another.
	e.Sections = append([]ReflectionSection(nil), e.Sections...)

	for i := range e.Sections {
		if e.Sections[i].Follow == nil {
			continue
		}
		section := &e.Sections[i]
		section.Follow = &f
		section.setBody(f.render())
		return
	}
	e.Sections = append(e.Sections, newFollowThroughSection(f))
}

// upgradeToV2 stamps v2 metadata and rewrites raw canonically. Only the entry
// it is called on changes; every other entry in the document keeps its bytes.
func (e *ReflectionEntry) upgradeToV2() {
	e.Metadata.Present = true
	e.Metadata.Version = ReflectionSchemaVersion
	e.RebuildRaw(RenderCanonicalV2)
}

// rebuildRaw replaces raw with freshly rendered text for this entry.
func (e *ReflectionEntry) RebuildRaw(mode RenderMode) {
	e.Raw = trimEntryFraming(e.render(mode))
	if lines := splitReflectionLines(e.Raw); len(lines) > 0 {
		if m := dateRe.FindStringSubmatch(lines[0]); m != nil {
			e.Date = m[1]
		}
	}
}

// BuildReflectionEntry assembles a fresh entry from configured questions.
func BuildReflectionEntry(date string, questions, answers []string, anchorQuestionIndex int) ReflectionEntry {
	e := ReflectionEntry{Date: date, HeadingLine: entryHeadingPrefix + date}
	e.ApplyAnswers(questions, answers, anchorQuestionIndex)
	return e
}
