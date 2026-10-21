package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
)

// Coverage is the one line in a rendered review whose meaning depends on what
// the window was built from: days answered out of days spanned for a week, and
// a count of the reviews synthesized for a quarter. All three renderers have to
// read it from the window rather than compose it themselves, or a quarterly
// review would report the day coverage it deliberately does not measure.

// renderedReview is one review shown every way Seneca shows it: on a terminal,
// piped, and stored in the archive.
func renderedReview(window reviewWindow) map[string]string {
	review := reviewResponse{
		Struggles:           []reviewItem{{Theme: "Late starts", Summary: "Mornings kept slipping."}},
		FocusRecommendation: "Protect the first hour.",
	}
	return map[string]string{
		"TTY":      renderReviewTTY(window, review, reviewFocusContext{}),
		"plain":    renderReviewPlain(window, review, reviewFocusContext{}),
		"Markdown": renderReviewMarkdown(window, review, reviewFocusContext{}, reviewDecision{kind: reviewSaveWithoutFocus}),
	}
}

func TestEveryRendererReadsCoverageFromTheWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		window reviewWindow
		want   string
		absent string
	}{
		{
			name: "an entry window still measures days",
			window: reviewWindow{
				Title:        "Weekly review",
				PeriodLabel:  "2026-08-17 to 2026-08-23",
				CoveredDays:  3,
				ExpectedDays: 7,
			},
			want:   "Coverage 3/7 days",
			absent: "source review",
		},
		{
			name: "an arc window counts its sources instead",
			window: reviewWindow{
				Title:        "Quarterly review",
				PeriodLabel:  "2026-07-01 to 2026-09-30",
				SourceKind:   sourceReviews,
				ExpectedDays: 92,
				Reviews:      make([]engine.ReviewRecord, 3),
			},
			want: "3 source reviews",
			// ExpectedDays stays factual on an arc window; no renderer shows it.
			absent: "days",
		},
		{
			name: "a lone source reads in the singular",
			window: reviewWindow{
				Title:       "Yearly review",
				PeriodLabel: "2026-01-01 to 2026-12-31",
				SourceKind:  sourceReviews,
				Reviews:     make([]engine.ReviewRecord, 1),
			},
			want:   "1 source review",
			absent: "days",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for renderer, out := range renderedReview(tc.window) {
				if !strings.Contains(out, tc.want) {
					t.Fatalf("expected the %s renderer to show %q, got:\n%s", renderer, tc.want, out)
				}
				if strings.Contains(out, tc.absent) {
					t.Fatalf("expected the %s renderer to omit %q, got:\n%s", renderer, tc.absent, out)
				}
			}
		})
	}
}

// The coverage line travels with the period label in the two renderers that
// carry both, so a reader always knows which span the count belongs to.
func TestRenderersKeepCoverageBesideThePeriodLabel(t *testing.T) {
	t.Parallel()

	window := reviewWindow{
		Title:       "Quarterly review",
		PeriodLabel: "2026-07-01 to 2026-09-30",
		SourceKind:  sourceReviews,
		Reviews:     make([]engine.ReviewRecord, 2),
	}
	rendered := renderedReview(window)

	for renderer, want := range map[string]string{
		"TTY":      "2026-07-01 to 2026-09-30  2 source reviews",
		"Markdown": "2026-07-01 to 2026-09-30  ·  2 source reviews",
	} {
		if !strings.Contains(rendered[renderer], want) {
			t.Fatalf("expected the %s renderer to show %q, got:\n%s", renderer, want, rendered[renderer])
		}
	}

	// The plain renderer puts the label on its own line, with coverage under it.
	if !strings.Contains(rendered["plain"], "2026-07-01 to 2026-09-30\n2 source reviews\n") {
		t.Fatalf("expected the plain renderer to stack label and coverage, got:\n%s", rendered["plain"])
	}
}
