package engine

import (
	"fmt"
	"sort"
	"strings"
)

// A share packet is what the author sends to another person: a sponsor, a
// director, a friend. It is usually a saved review alone, optionally with the
// entries of chosen days. It is written for a reader, not for Seneca: it names
// no file and no record identity, and carries none of Seneca's markers. The
// writing itself is kept as the author wrote it, under its own headings.

// SharePacket is one packet, in Markdown, with what it holds.
type SharePacket struct {
	Title string
	// FileName is a name for the packet's file, without an extension.
	FileName string
	Markdown string
	// ReviewID is the review it holds, empty when it holds none.
	ReviewID string
	// Dates are the days whose entries it holds, in order.
	Dates []string
}

// ShareSelectionError refuses a packet that would hold nothing, or name a
// day or a review the journal does not have.
type ShareSelectionError struct {
	NotFound bool
	message  string
}

func (e *ShareSelectionError) Error() string { return e.message }

// SharePacket assembles the packet of the review reviewID, when it is not
// empty, and of the entries of dates. It reads and writes nothing else: a
// day the journal does not hold is refused, never left out in silence.
func (j *Journal) SharePacket(reviewID string, dates []string) (SharePacket, error) {
	if reviewID == "" && len(dates) == 0 {
		return SharePacket{}, &ShareSelectionError{message: "a packet holds a review, days, or both; nothing was chosen"}
	}
	var review *ReviewRecord
	if reviewID != "" {
		reviews, err := j.Reviews()
		if err != nil {
			return SharePacket{}, err
		}
		for i := range reviews {
			if reviews[i].ID == reviewID {
				review = &reviews[i]
				break
			}
		}
		if review == nil {
			return SharePacket{}, &ShareSelectionError{NotFound: true, message: fmt.Sprintf("the archive has no review %s", reviewID)}
		}
	}
	var entries []ReflectionEntry
	if len(dates) > 0 {
		all, err := j.Entries()
		if err != nil {
			return SharePacket{}, err
		}
		if entries, err = selectShareEntries(all, dates); err != nil {
			return SharePacket{}, err
		}
	}
	return BuildSharePacket(review, entries, j.Today()), nil
}

func selectShareEntries(all []ReflectionEntry, dates []string) ([]ReflectionEntry, error) {
	byDate := make(map[string]ReflectionEntry, len(all))
	for _, entry := range all {
		byDate[entry.Date] = entry
	}
	seen := map[string]bool{}
	var selected []ReflectionEntry
	for _, date := range dates {
		if !IsCalendarDate(date) {
			return nil, &ShareSelectionError{message: fmt.Sprintf("%q is not a calendar date (YYYY-MM-DD)", date)}
		}
		if seen[date] {
			continue
		}
		seen[date] = true
		entry, ok := byDate[date]
		if !ok {
			return nil, &ShareSelectionError{NotFound: true, message: fmt.Sprintf("the journal has no entry for %s", date)}
		}
		selected = append(selected, entry)
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].Date < selected[j].Date })
	return selected, nil
}

// BuildSharePacket lays the packet out: a title, a line saying what it holds
// and when it was shared, the review, then each entry without Seneca's
// markers. The same selection on the same day always gives the same packet.
func BuildSharePacket(review *ReviewRecord, entries []ReflectionEntry, today string) SharePacket {
	packet := SharePacket{}
	for _, entry := range entries {
		packet.Dates = append(packet.Dates, entry.Date)
	}
	days := ""
	switch n := len(packet.Dates); {
	case n == 1:
		days = packet.Dates[0]
	case n > 1:
		days = packet.Dates[0] + " to " + packet.Dates[n-1]
	}

	var holds []string
	switch {
	case review != nil:
		packet.ReviewID = review.ID
		packet.Title = review.Title
		packet.FileName = "Seneca " + strings.ToLower(string(review.Period)) + " review " + review.Start + " to " + review.End
		holds = append(holds, "a "+strings.ToLower(string(review.Period))+" review")
	default:
		packet.Title = "Reflections, " + days
		packet.FileName = "Seneca reflections " + days
	}
	if len(entries) > 0 {
		holds = append(holds, "the entries of "+Plural(len(entries), "day", "days")+": "+strings.Join(packet.Dates, ", "))
	}

	var b strings.Builder
	b.WriteString("# " + packet.Title + "\n\n")
	fmt.Fprintf(&b, "Shared from a Seneca journal on %s. It holds %s.\n", today, strings.Join(holds, ", and "))
	if review != nil {
		b.WriteString("\n---\n\n")
		b.WriteString(strings.TrimSpace(review.Body))
		b.WriteString("\n")
	}
	for _, entry := range entries {
		b.WriteString("\n---\n\n")
		b.WriteString(strings.TrimRight(entry.render(renderLegacy), "\n"))
		b.WriteString("\n")
	}
	packet.Markdown = b.String()
	return packet
}
