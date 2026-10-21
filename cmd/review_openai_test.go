package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/review/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Like the Anthropic tests, these never reach the real API or the real
// Keychain: the SDK is pointed at a local Responses API.

const fakeOpenAIKey = "sk-openai-test-key"

// fakeOpenAI is a Responses API that answers with one scripted response.
type fakeOpenAI struct {
	text string
	// status defaults to "completed".
	status string
	// incompleteReason fills incomplete_details when set.
	incompleteReason string
	// refusal replaces the output text with a refusal content item.
	refusal string
	// hang holds the request open until the client gives up.
	hang bool

	mu       sync.Mutex
	requests []fakeOpenAIRequest
}

type fakeOpenAIRequest struct {
	authorization string
	body          map[string]any
}

func (f *fakeOpenAI) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, fakeOpenAIRequest{authorization: r.Header.Get("Authorization"), body: body})
		f.mu.Unlock()

		if f.hang {
			<-r.Context().Done()
			return
		}

		status := f.status
		if status == "" {
			status = "completed"
		}
		incomplete := "null"
		if f.incompleteReason != "" {
			incomplete = `{"reason":"` + f.incompleteReason + `"}`
		}
		content, _ := json.Marshal(map[string]any{"type": "output_text", "text": f.text, "annotations": []any{}})
		if f.refusal != "" {
			content, _ = json.Marshal(map[string]any{"type": "refusal", "refusal": f.refusal})
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"resp_test","object":"response","created_at":0,"status":%q,"model":"gpt-6-astra",`+
			`"output":[{"type":"reasoning","id":"rs_test","summary":[]},`+
			`{"type":"message","id":"msg_test","role":"assistant","status":"completed","content":[%s]}],`+
			`"incomplete_details":%s,"error":null,`+
			`"usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":4},"output_tokens":42,"output_tokens_details":{"reasoning_tokens":30},"total_tokens":54}}`,
			status, content, incomplete)
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeOpenAI) only(t *testing.T) fakeOpenAIRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		t.Fatalf("expected exactly one Responses request, got %d", len(f.requests))
	}
	return f.requests[0]
}

func openAIConfig() reviewConfig {
	return reviewConfig{Harness: harnessOpenAI, Model: provider.OpenAIDefaultModel, Effort: provider.OpenAIDefaultEffort}
}

func TestOpenAIReviewSavesTheSameRecordAsTheClaudeHarness(t *testing.T) {
	payload := reviewPayload(`{"summary":"You mostly held the focus.","evidence":[{"date":"2026-08-20","snippet":"Worked on the archive."}]}`)
	decision := reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation}
	now := archiveTime(24, 20, 0)

	server := (&fakeOpenAI{text: payload}).start(t)
	providerPath := archiveWithFocus(t, 18, "Ship the archive layer.")
	viaProvider := &fakeReview{tty: true, decision: decision}
	reviewer := providerReviewer{getenv: envWith(map[string]string{"OPENAI_API_KEY": fakeOpenAIKey}), keychain: noKeychain, baseURL: server.URL}
	if err := executeReview(providerRuntime(viaProvider, now, reviewer), openAIConfig(), providerPath, savedWindow(t)); err != nil {
		t.Fatalf("openai review returned error: %v", err)
	}

	harnessPath := archiveWithFocus(t, 18, "Ship the archive layer.")
	viaHarness := &fakeReview{
		tty:      true,
		decision: decision,
		response: `[{"type":"system"},{"type":"result","structured_output":` + payload + `}]`,
	}
	if err := executeReview(viaHarness.runtime(now), reviewConfig{Harness: harnessClaude}, harnessPath, savedWindow(t)); err != nil {
		t.Fatalf("claude harness review returned error: %v", err)
	}

	if got, want := readFileString(t, providerPath), readFileString(t, harnessPath); got != want {
		t.Fatalf("the provider's archive differs from the harness's.\nprovider:\n%s\nharness:\n%s", got, want)
	}
	if !strings.Contains(readFileString(t, providerPath), "You mostly held the focus.") {
		t.Fatal("expected the review to be saved")
	}
	if viaProvider.prompts[0] != viaHarness.prompts[0] {
		t.Fatal("expected the provider and the harness to receive the same prompt")
	}
}

func TestOpenAIRequestCarriesTheModelEffortAndStrictSchema(t *testing.T) {
	fake := &fakeOpenAI{text: reviewPayload("null")}
	server := fake.start(t)
	reviewer := providerReviewer{getenv: envWith(nil), keychain: func(service string) (string, error) {
		if service != "seneca-openai" {
			return "", provider.ErrKeychainItemNotFound
		}
		return "sk-from-keychain\n", nil
	}, baseURL: server.URL}

	review := &fakeReview{tty: false}
	cfg := reviewConfig{Harness: harnessOpenAI, Model: "gpt-6-mini", Effort: "xhigh"}
	if err := executeReview(providerRuntime(review, archiveTime(24, 20, 0), reviewer), cfg, newArchivePath(t), savedWindow(t)); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}

	request := fake.only(t)
	if request.authorization != "Bearer sk-from-keychain" {
		t.Fatalf("expected the Keychain key to be sent, got %q", request.authorization)
	}
	body := request.body
	if body["model"] != "gpt-6-mini" {
		t.Fatalf("expected the configured model, got %v", body["model"])
	}
	if body["store"] != false {
		t.Fatalf("expected store=false, got %v", body["store"])
	}
	if input, _ := body["input"].(string); input == "" {
		t.Fatalf("expected the prompt as the input, got %v", body["input"])
	}
	if reasoning, _ := body["reasoning"].(map[string]any); reasoning["effort"] != "xhigh" {
		t.Fatalf("expected reasoning effort xhigh, got %v", body["reasoning"])
	}
	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "seneca_review" {
		t.Fatalf("expected a strict json_schema format named seneca_review, got %v", format)
	}
	var want any
	if err := json.Unmarshal([]byte(reviewSchema), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(format["schema"], want) {
		t.Fatalf("expected the review schema verbatim, got %v", format["schema"])
	}

	if !strings.Contains(review.out.String(), savedFocusRecommendation) {
		t.Fatalf("expected the plain report on stdout, got:\n%s", review.out.String())
	}
	if review.decisions != 0 {
		t.Fatal("expected a non-TTY review never to ask for a decision")
	}
}

func TestOpenAIFailuresWriteNothing(t *testing.T) {
	validKey := envWith(map[string]string{"OPENAI_API_KEY": fakeOpenAIKey})
	cases := []struct {
		name    string
		fake    *fakeOpenAI
		getenv  func(string) string
		timeout time.Duration
		want    []string
		check   func(t *testing.T, err error)
	}{
		{
			name:   "schema-invalid reply",
			fake:   &fakeOpenAI{text: `{"focus_assessment":null,"struggles":[]}`},
			getenv: validKey,
			want:   []string{"parsing openai review response", "does not match the requested schema"},
		},
		{
			name:   "refusal",
			fake:   &fakeOpenAI{refusal: "I can't help with that."},
			getenv: validKey,
			want:   []string{"gpt-6-astra declined this review", "I can't help with that.", "--model", "nothing was written"},
			check: func(t *testing.T, err error) {
				var refusal *provider.RefusalError
				if !errors.As(err, &refusal) {
					t.Fatalf("expected a typed refusal, got %T", err)
				}
			},
		},
		{
			name:   "content filter",
			fake:   &fakeOpenAI{text: `{"focus_assessment":`, status: "incomplete", incompleteReason: "content_filter"},
			getenv: validKey,
			want:   []string{"declined this review", "category content_filter", "nothing was written"},
		},
		{
			name:   "truncated reply",
			fake:   &fakeOpenAI{text: `{"focus_assessment":`, status: "incomplete", incompleteReason: "max_output_tokens"},
			getenv: validKey,
			want:   []string{"stopped at its output limit", "--effort", "nothing was written"},
			check: func(t *testing.T, err error) {
				var truncated *provider.TruncatedError
				if !errors.As(err, &truncated) {
					t.Fatalf("expected a typed truncation, got %T", err)
				}
			},
		},
		{
			name:    "timeout",
			fake:    &fakeOpenAI{hang: true},
			getenv:  validKey,
			timeout: 200 * time.Millisecond,
			want:    []string{"openai harness exceeded its 200ms time limit", "--timeout", "nothing was written"},
		},
		{
			name:   "missing key",
			fake:   &fakeOpenAI{text: reviewPayload("null")},
			getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}),
			want:   []string{"no API key", "OPENAI_API_KEY", "security add-generic-password", "seneca-openai"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.fake.start(t)
			path := archiveWithFocus(t, 18, "Ship the archive layer.")
			before := readFileString(t, path)

			review := &fakeReview{tty: true, decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation}}
			reviewer := providerReviewer{getenv: tc.getenv, keychain: noKeychain, baseURL: server.URL}
			cfg := openAIConfig()
			cfg.Timeout = tc.timeout
			err := executeReview(providerRuntime(review, archiveTime(24, 20, 0), reviewer), cfg, path, savedWindow(t))
			if err == nil {
				t.Fatal("expected the review to fail")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("expected the error to contain %q, got: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), fakeOpenAIKey) {
				t.Fatal("the error must never print the key")
			}
			if tc.check != nil {
				tc.check(t, err)
			}
			if review.decisions != 0 {
				t.Fatal("expected no decision prompt after a failed reply")
			}
			if readFileString(t, path) != before {
				t.Fatal("expected the archive to stay byte-identical")
			}
		})
	}
}

func TestOpenAIRejectedKeyNamesItsSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`)
	}))
	t.Cleanup(server.Close)

	reviewer := providerReviewer{getenv: envWith(map[string]string{"OPENAI_API_KEY": fakeOpenAIKey}), keychain: noKeychain, baseURL: server.URL}
	_, err := reviewer.review(openAIConfig(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "the OpenAI API rejected the key from OPENAI_API_KEY") {
		t.Fatalf("expected the rejected key to name its source, got %v", err)
	}
	if strings.Contains(err.Error(), fakeOpenAIKey) {
		t.Fatal("the error must never print the key")
	}
}

func TestResolveKeyReadsTheOpenAISources(t *testing.T) {
	t.Parallel()

	key, label, err := provider.ResolveKey(provider.OpenAIKey, envWith(map[string]string{"OPENAI_API_KEY": "from-env", "ANTHROPIC_API_KEY": "other"}), noKeychain)
	if err != nil || key != "from-env" || label != "OPENAI_API_KEY" {
		t.Fatalf("expected the OpenAI environment key, got key=%q label=%q err=%v", key, label, err)
	}
	key, label, err = provider.ResolveKey(provider.OpenAIKey, envWith(nil), func(service string) (string, error) {
		if service != "seneca-openai" {
			return "", provider.ErrKeychainItemNotFound
		}
		return "from-keychain", nil
	})
	if err != nil || key != "from-keychain" || label != "Keychain (seneca-openai)" {
		t.Fatalf("expected the OpenAI Keychain key, got key=%q label=%q err=%v", key, label, err)
	}
}

func TestResolveReviewConfigOpenAIDefaultsAndEffort(t *testing.T) {
	t.Parallel()

	cfg, err := resolveReviewConfig("openai", true, "", false, "", false, defaultReviewTimeout, envWith(nil), reviewSettings{})
	if err != nil || cfg.Harness != harnessOpenAI || cfg.Model != "gpt-6-astra" || cfg.Effort != "high" {
		t.Fatalf("expected openai/gpt-6-astra/high, got %+v, %v", cfg, err)
	}

	cfg, err = resolveReviewConfig("", false, "", false, "", false, defaultReviewTimeout,
		envWith(map[string]string{"SENECA_HARNESS": "OpenAI", "SENECA_EFFORT": "low", "SENECA_MODEL": "gpt-6-mini"}), reviewSettings{Effort: "max"})
	if err != nil || cfg.Harness != harnessOpenAI || cfg.Model != "gpt-6-mini" || cfg.Effort != "low" {
		t.Fatalf("expected openai/gpt-6-mini/low, got %+v, %v", cfg, err)
	}

	_, err = resolveReviewConfig("codex", true, "", false, "high", true, defaultReviewTimeout, envWith(nil), reviewSettings{})
	if err == nil || !strings.Contains(err.Error(), "choose --harness anthropic or openai") {
		t.Fatalf("expected the refusal to name both providers, got %v", err)
	}
}

func TestDoctorReportsTheOpenAIKeySourceNeverTheKey(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, "")

	for _, tc := range []struct {
		name     string
		env      map[string]string
		keychain provider.KeychainReader
		level    doctorSeverity
		want     string
	}{
		{name: "environment", env: map[string]string{"OPENAI_API_KEY": fakeOpenAIKey}, keychain: noKeychain, level: doctorOK, want: "harness openai (from SENECA_HARNESS) uses the API key from OPENAI_API_KEY (not called)"},
		{name: "keychain", keychain: func(service string) (string, error) {
			if service != "seneca-openai" {
				return "", provider.ErrKeychainItemNotFound
			}
			return fakeOpenAIKey, nil
		}, level: doctorOK, want: "uses the API key from Keychain (seneca-openai)"},
		{name: "missing despite an Anthropic key", env: map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}, keychain: noKeychain, level: doctorWarn, want: "harness openai (from SENECA_HARNESS) has no API key: set OPENAI_API_KEY or store one in the Keychain as seneca-openai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := h.env()
			values := map[string]string{"SENECA_FILE": h.reflections, "SENECA_HARNESS": "openai"}
			for key, value := range tc.env {
				values[key] = value
			}
			env.getenv = envWith(values)
			env.keychain = tc.keychain

			report := diagnose(env)
			requireDoctor(t, report, tc.level, tc.want)
			for _, result := range report.results {
				if strings.Contains(result.detail, fakeOpenAIKey) {
					t.Fatalf("doctor printed the key: %s", result.detail)
				}
			}
		})
	}
}
