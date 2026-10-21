package cmd

import (
	"strings"
	"testing"
)

// strictPayload is a complete, schema-valid reply whose parts the strictness
// cases below damage one at a time.
func strictPayload(assessment, extra string) string {
	return `{"focus_assessment":` + assessment + `,` +
		`"struggles":[{"theme":"Late starts","summary":"Deep work slipped past noon.",` +
		`"evidence":[{"date":"2026-08-20","snippet":"Started after lunch."}]}],` +
		`"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."` + extra + `}`
}

// The declared schema is the contract Seneca hands the harness, so it is also
// the contract the reply is held to locally: unknown properties, omitted
// required ones, wrong types, and a second JSON value are all refused before
// anything is rendered or saved.
func TestParseReviewResponseEnforcesStrictSchema(t *testing.T) {
	t.Parallel()

	withFocus := reviewGrounding{
		entryDates: []string{"2026-08-20"},
		focus:      focusContext("Ship the archive layer.", "2026-08-18", "2026-08-20"),
	}
	noFocus := groundingForDates("2026-08-20")

	cases := []struct {
		name      string
		payload   string
		grounding reviewGrounding
		wantErr   string
	}{
		{
			name:      "unknown top-level property",
			payload:   strictPayload("null", `,"confidence":0.8`),
			grounding: noFocus,
			wantErr:   `the response has an unknown property "confidence"`,
		},
		{
			name: "unknown property inside an item",
			payload: `{"focus_assessment":null,"struggles":[{"theme":"Late starts",` +
				`"summary":"Deep work slipped.","evidence":[],"severity":"high"}],` +
				`"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   `struggles[0] has an unknown property "severity"`,
		},
		{
			name: "unknown property inside evidence",
			payload: `{"focus_assessment":null,"struggles":[{"theme":"Late starts",` +
				`"summary":"Deep work slipped.","evidence":[{"date":"2026-08-20",` +
				`"snippet":"Started after lunch.","source":"invented"}]}],` +
				`"wins":[],"carry_forward":[],"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   `struggles[0].evidence[0] has an unknown property "source"`,
		},
		{
			name: "omitted focus_assessment",
			payload: `{"struggles":[],"wins":[],"carry_forward":[],` +
				`"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   "missing focus_assessment",
		},
		{
			name: "omitted required section",
			payload: `{"focus_assessment":null,"struggles":[],"carry_forward":[],` +
				`"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   "missing wins",
		},
		{
			name: "omitted required item field",
			payload: `{"focus_assessment":null,"struggles":[{"summary":"Deep work slipped.",` +
				`"evidence":[]}],"wins":[],"carry_forward":[],` +
				`"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   "missing struggles[0].theme",
		},
		{
			name: "section sent as an object",
			payload: `{"focus_assessment":null,"struggles":{},"wins":[],"carry_forward":[],` +
				`"focus_recommendation":"Protect the first hour."}`,
			grounding: noFocus,
			wantErr:   "struggles must be an array, got object",
		},
		{
			name: "recommendation sent as a number",
			payload: `{"focus_assessment":null,"struggles":[],"wins":[],"carry_forward":[],` +
				`"focus_recommendation":42}`,
			grounding: noFocus,
			wantErr:   "focus_recommendation must be a string, got number",
		},
		{
			name:      "assessment sent as a string",
			payload:   strictPayload(`"looked fine"`, ""),
			grounding: withFocus,
			wantErr:   "focus_assessment must be an object or null, got string",
		},
		{
			name:      "evidence date sent as a number",
			payload:   strictPayload(`{"summary":"You held it.","evidence":[{"date":20260820,"snippet":"Held it."}]}`, ""),
			grounding: withFocus,
			wantErr:   "focus_assessment.evidence[0].date must be a string, got number",
		},
		{
			name:      "trailing JSON after the response",
			payload:   strictPayload("null", "") + strictPayload("null", ""),
			grounding: noFocus,
			wantErr:   "unexpected content after the response",
		},
		{
			name:      "explicit null while a focus applies",
			payload:   strictPayload("null", ""),
			grounding: withFocus,
			wantErr:   "missing focus_assessment: a focus set on 2026-08-18",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseReviewResponse([]byte(tc.payload), tc.grounding)
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}

	t.Run("explicit null with no focus is accepted", func(t *testing.T) {
		t.Parallel()

		response, err := parseReviewResponse([]byte(strictPayload("null", "")), noFocus)
		if err != nil {
			t.Fatalf("parseReviewResponse returned error: %v", err)
		}
		if response.FocusAssessment != nil {
			t.Fatalf("expected no assessment, got %+v", response.FocusAssessment)
		}
	})

	// The structured-output envelope is not a way around the schema: it reaches
	// the same gate the bare and fenced payloads do.
	t.Run("the Claude envelope is held to the same schema", func(t *testing.T) {
		t.Parallel()

		envelope := `[{"type":"system"},{"type":"result","structured_output":` +
			strictPayload("null", `,"confidence":0.8`) + `}]`

		_, err := parseReviewResponse([]byte(envelope), noFocus)
		if err == nil || !strings.Contains(err.Error(), `unknown property "confidence"`) {
			t.Fatalf("expected the envelope payload to be refused, got %v", err)
		}
	})

	t.Run("a fenced payload is held to the same schema", func(t *testing.T) {
		t.Parallel()

		fenced := "```json\n" + strictPayload("null", `,"confidence":0.8`) + "\n```"

		_, err := parseReviewResponse([]byte(fenced), noFocus)
		if err == nil || !strings.Contains(err.Error(), `unknown property "confidence"`) {
			t.Fatalf("expected the fenced payload to be refused, got %v", err)
		}
	})
}

// The validator enforces exactly the keywords reviewSchema uses; a constraint it
// cannot enforce must fail to compile rather than pass silently.
func TestCompileJSONSchemaRefusesWhatItCannotEnforce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "unsupported keyword", raw: `{"type":"object","properties":{},"minProperties":1}`},
		{name: "missing type", raw: `{"properties":{"a":{"type":"string"}}}`},
		{name: "unsupported type", raw: `{"type":"integer"}`},
		{name: "required property with no definition", raw: `{"type":"object","properties":{},"required":["a"]}`},
		{name: "trailing content", raw: `{"type":"object"}{"type":"string"}`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := compileJSONSchema(tc.raw); err == nil {
				t.Fatalf("expected %s to be refused", tc.name)
			}
		})
	}

	if compiledReviewSchema == nil {
		t.Fatal("expected the review schema to compile")
	}
}
