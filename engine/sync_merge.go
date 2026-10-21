package engine

import (
	"fmt"
	"strings"
)

// Merging is deciding what an artifact holds after two devices changed it
// without seeing each other. The answer is either one revision both devices
// can compute alone and agree on, or a conflict: no merge ever guesses.
//
// # Revisions
//
// Three revisions go in: the one both changes were made on, ours, and theirs.
// A side that left the base alone yields to the other, and two sides that
// arrived at the same bytes agree. That much holds for every artifact.
//
// # Entries
//
// Only a day's entry is merged further, and only by its parts: the lines
// above the first section, and each `### ` section together with the markers
// bound to its heading. A part is taken whole from the side that changed it;
// nothing inside a part is ever combined, so an answer keeps its markers and a
// follow-through keeps its source, its snapshot, and its response together.
// Parts are matched by exact heading. An entry with a repeated heading, a
// marker Seneca cannot read, or other line endings than LF has no unique
// parts, and is not merged.
//
// Both sides may change different parts. One side may add or remove sections
// while the other keeps the sections the base had. Two sides that both changed
// which sections there are, a part both changed differently, and a section one
// side removed and the other changed are conflicts.
//
// The merged entry is its parts separated by one blank line, the way Seneca
// writes an entry; the bytes of each part are the author's.
//
// # Created twice
//
// Two entries written independently for one day are one entry only when they
// say the same thing: the same parts, apart from the `seneca:entry` marker
// that records when each was created. Anything else is a conflict.
//
// # Everything else
//
// An archive record is immutable and the journal settings are one unit, so two
// different revisions of either are a conflict, as is a deletion beside an
// edit.

// entryPart is one mergeable part of an entry: the lines above the first
// section, or one section with the markers bound to it.
type entryPart struct {
	// key is the section's heading text. The first part has none.
	key string
	// text is the part's lines, without the blank lines that trail it.
	text string
}

// entryParts splits an entry into its parts, following the same binding rules
// as the parser. problem says why the entry has no unique parts.
func entryParts(raw string) (parts []entryPart, problem string) {
	if strings.ContainsRune(raw, '\r') {
		return nil, "it uses line endings other than LF"
	}
	entry := NewReflectionEntry(raw)
	if entry.Raw != raw {
		return nil, "it is not exactly one entry"
	}
	if len(entry.Diagnostics) > 0 {
		return nil, "its structure is ambiguous: " + entry.Diagnostics[0].String()
	}

	groups := [][]string{nil}
	keys := []string{""}
	var pending []string
	last := func() *[]string { return &groups[len(groups)-1] }
	for i, line := range strings.Split(raw, "\n") {
		if i == 0 {
			*last() = append(*last(), line)
			continue
		}
		if heading, ok := sectionHeadingText(line); ok {
			groups = append(groups, append(pending, line))
			keys = append(keys, heading)
			pending = nil
			continue
		}
		if marker, ok := parseReflectionMarker(line); ok {
			if marker.kind == markerKindEntry && !marker.malformed && len(groups) == 1 && len(pending) == 0 {
				*last() = append(*last(), line)
				continue
			}
			pending = append(pending, line)
			continue
		}
		*last() = append(*last(), pending...)
		*last() = append(*last(), line)
		pending = nil
	}
	*last() = append(*last(), pending...)

	for i, lines := range groups {
		end := len(lines)
		for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		parts = append(parts, entryPart{key: keys[i], text: strings.Join(lines[:end], "\n")})
	}
	return parts, ""
}

// partName names a part for a person.
func partName(index int, key string) string {
	if index == 0 {
		return "the lines above the first section"
	}
	return fmt.Sprintf("the section %q", key)
}

func joinEntryParts(parts []entryPart) string {
	texts := make([]string, len(parts))
	for i, part := range parts {
		texts[i] = part.text
	}
	return strings.Join(texts, "\n\n")
}

// sectionKeys lists the headings of an entry's sections, in order.
func sectionKeys(parts []entryPart) []string {
	keys := make([]string, 0, len(parts))
	for _, part := range parts[1:] {
		keys = append(keys, part.key)
	}
	return keys
}

// sectionTexts maps each section heading to its part. The parts were read from
// an entry without a repeated heading, so each heading has one.
func sectionTexts(parts []entryPart) map[string]string {
	texts := make(map[string]string, len(parts))
	for _, part := range parts[1:] {
		texts[part.key] = part.text
	}
	return texts
}

// mergeEntry reconciles two revisions of one day's entry that were both made
// on base. It returns the merged entry, or why there is none. The result does
// not depend on which side is called ours.
func mergeEntry(base, ours, theirs string) (merged string, conflict string) {
	if ours == theirs {
		return ours, ""
	}
	sides := [3][]entryPart{}
	for i, raw := range []string{base, ours, theirs} {
		parts, problem := entryParts(raw)
		if problem != "" {
			return "", "an entry cannot be merged by its sections: " + problem
		}
		sides[i] = parts
	}
	b, o, t := sides[0], sides[1], sides[2]
	baseKeys, ourKeys, theirKeys := sectionKeys(b), sectionKeys(o), sectionKeys(t)

	var shape []string
	switch {
	case equalStringSlices(ourKeys, theirKeys):
		shape = ourKeys
	case equalStringSlices(ourKeys, baseKeys):
		shape = theirKeys
	case equalStringSlices(theirKeys, baseKeys):
		shape = ourKeys
	default:
		return "", "both devices changed which sections the entry has"
	}

	baseText, ourText, theirText := sectionTexts(b), sectionTexts(o), sectionTexts(t)
	kept := make(map[string]bool, len(shape))
	for _, key := range shape {
		kept[key] = true
	}
	for _, key := range baseKeys {
		if kept[key] {
			continue
		}
		for _, side := range []map[string]string{ourText, theirText} {
			if text, still := side[key]; still && text != baseText[key] {
				return "", fmt.Sprintf("one device removed the section %q and the other changed it", key)
			}
		}
	}

	// pick is the three-way choice for one part.
	pick := func(index int, key, was string, had bool, mine, yours string) (string, string) {
		switch {
		case mine == yours:
			return mine, ""
		case had && mine == was:
			return yours, ""
		case had && yours == was:
			return mine, ""
		default:
			return "", fmt.Sprintf("both devices changed %s", partName(index, key))
		}
	}

	head, problem := pick(0, "", b[0].text, true, o[0].text, t[0].text)
	if problem != "" {
		return "", problem
	}
	result := []entryPart{{text: head}}
	for i, key := range shape {
		mine, haveMine := ourText[key]
		yours, haveYours := theirText[key]
		was, had := baseText[key]
		var text string
		switch {
		case haveMine && haveYours:
			if text, problem = pick(i+1, key, was, had, mine, yours); problem != "" {
				return "", problem
			}
		case haveMine:
			text = mine
		default:
			text = yours
		}
		result = append(result, entryPart{key: key, text: text})
	}

	merged = joinEntryParts(result)
	// The merged entry must read back as exactly the parts it was built from;
	// otherwise joining them changed what one of them means.
	back, problem := entryParts(merged)
	if problem != "" || len(back) != len(result) {
		return "", "the merged entry would not read back as the parts it was made of"
	}
	for i := range back {
		if back[i] != result[i] {
			return "", "the merged entry would not read back as the parts it was made of"
		}
	}
	return merged, ""
}

// equivalentEntries reports whether two entries written independently for one
// day say the same thing: the same parts, apart from the marker recording when
// each was created.
func equivalentEntries(ours, theirs string) bool {
	if ours == theirs {
		return true
	}
	mine, problem := entryParts(ours)
	if problem != "" {
		return false
	}
	yours, problem := entryParts(theirs)
	if problem != "" || len(mine) != len(yours) {
		return false
	}
	mine[0].text, yours[0].text = withoutEntryMarker(mine[0].text), withoutEntryMarker(yours[0].text)
	for i := range mine {
		if mine[i] != yours[i] {
			return false
		}
	}
	return true
}

func withoutEntryMarker(head string) string {
	lines := strings.Split(head, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if marker, ok := parseReflectionMarker(line); ok && marker.kind == markerKindEntry && !marker.malformed {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mergeOutcome is what merging two revisions of an artifact came to.
type mergeOutcome struct {
	// clean is false for a conflict; reason then says why.
	clean  bool
	reason string
	// revision is the merged revision. content holds its bytes when it is
	// neither side's own.
	revision Revision
	content  []byte
}

const (
	conflictDeletedAndChanged = "it was deleted on one device and changed on another"
	conflictCreatedTwice      = "it was written independently on two devices, and the two do not say the same thing"
	conflictRecordDiffers     = "an archive record is never rewritten, and two devices hold different bytes for this one"
	conflictSettingsDiffer    = "the journal settings were changed on two devices; they are one unit and are not combined"
	conflictSeveralVersions   = "more than two versions of it compete"
	conflictSettledTwice      = "two devices settled it differently, each without seeing the other's answer"
)

// merge reconciles ours and theirs, two revisions of artifact held in this
// device's history, given the revision both were made on.
func (w *syncWork) merge(artifact string, base, ours, theirs Revision) mergeOutcome {
	switch {
	case ours == theirs, theirs == base:
		return mergeOutcome{clean: true, revision: ours}
	case ours == base:
		return mergeOutcome{clean: true, revision: theirs}
	}
	source, _ := artifactSource(artifact)
	switch {
	case source == syncSourceArchive:
		return mergeOutcome{reason: conflictRecordDiffers}
	case source == syncSourceSettings:
		return mergeOutcome{reason: conflictSettingsDiffer}
	case ours == RevisionAbsent || theirs == RevisionAbsent:
		return mergeOutcome{reason: conflictDeletedAndChanged}
	}

	mine, err := w.object(ours)
	if err != nil {
		return mergeOutcome{reason: "this device's version is no longer held to merge from"}
	}
	yours, err := w.object(theirs)
	if err != nil {
		return mergeOutcome{reason: "the other device's version is not held to merge from"}
	}
	if base == RevisionAbsent {
		if !equivalentEntries(string(mine), string(yours)) {
			return mergeOutcome{reason: conflictCreatedTwice}
		}
		// Both devices must keep the same bytes, whichever one decides.
		if string(yours) < string(mine) {
			return mergeOutcome{clean: true, revision: theirs}
		}
		return mergeOutcome{clean: true, revision: ours}
	}
	was, err := w.object(base)
	if err != nil {
		return mergeOutcome{reason: "the version both changes were made on is not held to merge from"}
	}
	merged, conflict := mergeEntry(string(was), string(mine), string(yours))
	if conflict != "" {
		return mergeOutcome{reason: conflict}
	}
	content := []byte(merged)
	if err := validateArtifactContent(artifact, content); err != nil {
		return mergeOutcome{reason: "the merged entry is not one seneca would write: " + err.Error()}
	}
	return mergeOutcome{clean: true, revision: RevisionOf(content), content: content}
}
