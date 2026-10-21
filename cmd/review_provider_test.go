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

// These tests never reach the real API or the real Keychain: the SDK is
// pointed at a local server through its base-URL option, and the key comes
// from an injected environment or Keychain reader.

const fakeAnthropicKey = "sk-ant-test-key"

// fakeAnthropic is a Messages API that streams one scripted reply.
type fakeAnthropic struct {
	text       string
	stopReason string
	// stopDetails is the raw JSON for message_delta's stop_details.
	stopDetails string
	// hang holds the request open until the client gives up.
	hang bool
	// declined, when set, is the partial text a declining model streamed
	// before the server's fallback took over, followed by a fallback block.
	declined string

	mu       sync.Mutex
	requests []fakeAnthropicRequest
}

type fakeAnthropicRequest struct {
	apiKey string
	betas  string
	body   map[string]any
}

func (f *fakeAnthropic) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
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
		f.requests = append(f.requests, fakeAnthropicRequest{
			apiKey: r.Header.Get("X-Api-Key"),
			betas:  r.Header.Get("Anthropic-Beta"),
			body:   body,
		})
		f.mu.Unlock()

		if f.hang {
			<-r.Context().Done()
			return
		}

		stopReason := f.stopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		stopDetails := f.stopDetails
		if stopDetails == "" {
			stopDetails = "null"
		}
		text, _ := json.Marshal(f.text)

		w.Header().Set("Content-Type", "text/event-stream")
		events := []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_read_input_tokens":100,"cache_creation_input_tokens":8,"cache_creation":{"ephemeral_5m_input_tokens":8,"ephemeral_1h_input_tokens":0},"output_tokens":1}}}`},
		}
		index := 0
		if f.declined != "" {
			declined, _ := json.Marshal(f.declined)
			events = append(events, []struct{ name, data string }{
				{"content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index)},
				{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, index, declined)},
				{"content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)},
				{"content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"fallback","from":{"model":"claude-opus-5-5"},"to":{"model":"claude-opus-4-8"},"trigger":{"type":"refusal","category":"cyber"}}}`, index+1)},
				{"content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index+1)},
			}...)
			index += 2
		}
		events = append(events, []struct{ name, data string }{
			{"content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":"","signature":""}}`, index)},
			{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":"sig"}}`, index)},
			{"content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)},
			{"content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index+1)},
			{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, index+1, text)},
			{"content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index+1)},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_sequence":null,"stop_details":` + stopDetails + `},"usage":{"output_tokens":42,"output_tokens_details":{"thinking_tokens":30}}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}...)
		for _, event := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeAnthropic) only(t *testing.T) fakeAnthropicRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		t.Fatalf("expected exactly one Messages request, got %d", len(f.requests))
	}
	return f.requests[0]
}

func envWith(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func noKeychain(string) (string, error) { return "", provider.ErrKeychainItemNotFound }

// providerRuntime is fakeReview's runtime with the real provider path behind
// the harness seam, pointed at the fake server.
func providerRuntime(f *fakeReview, now time.Time, reviewer providerReviewer) reviewRuntime {
	runtime := f.runtime(now)
	runtime.harness = func(cfg reviewConfig, prompt string) (reviewReply, error) {
		f.prompts = append(f.prompts, prompt)
		return reviewer.review(cfg, prompt)
	}
	return runtime
}

func anthropicConfig() reviewConfig {
	return reviewConfig{Harness: harnessAnthropic, Model: provider.AnthropicDefaultModel, Effort: provider.AnthropicDefaultEffort}
}

func TestAnthropicReviewSavesTheSameRecordAsTheClaudeHarness(t *testing.T) {
	payload := reviewPayload(`{"summary":"You mostly held the focus.","evidence":[{"date":"2026-08-20","snippet":"Worked on the archive."}]}`)
	decision := reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation}
	now := archiveTime(24, 20, 0)

	server := (&fakeAnthropic{text: payload}).start(t)
	providerPath := archiveWithFocus(t, 18, "Ship the archive layer.")
	viaProvider := &fakeReview{tty: true, decision: decision}
	reviewer := providerReviewer{getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}), keychain: noKeychain, baseURL: server.URL}
	if err := executeReview(providerRuntime(viaProvider, now, reviewer), anthropicConfig(), providerPath, savedWindow(t)); err != nil {
		t.Fatalf("anthropic review returned error: %v", err)
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

// A declined model's partial text before the server's fallback block is
// abandoned output; only the fallback model's reply is parsed and saved.
func TestAnthropicFallbackDiscardsTheDeclinedModelsText(t *testing.T) {
	payload := reviewPayload(`{"summary":"You mostly held the focus.","evidence":[]}`)
	server := (&fakeAnthropic{declined: `{"focus_assessment":{"summary":"Abandoned`, text: payload}).start(t)
	path := archiveWithFocus(t, 18, "Ship the archive layer.")
	reviewer := providerReviewer{getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}), keychain: noKeychain, baseURL: server.URL}
	review := &fakeReview{tty: true, decision: reviewDecision{kind: reviewSaveWithoutFocus}}
	if _, err := quietly(t, func() error {
		return executeReview(providerRuntime(review, archiveTime(24, 20, 0), reviewer), anthropicConfig(), path, savedWindow(t))
	}); err != nil {
		t.Fatalf("expected the fallback model's review to save, got %v", err)
	}
	saved := readFileString(t, path)
	if !strings.Contains(saved, "You mostly held the focus.") || strings.Contains(saved, "Abandoned") {
		t.Fatalf("expected only the fallback model's review in the archive:\n%s", saved)
	}
}

func TestAnthropicRequestCarriesTheModelEffortSchemaAndFallback(t *testing.T) {
	fake := &fakeAnthropic{text: reviewPayload("null")}
	server := fake.start(t)
	reviewer := providerReviewer{getenv: envWith(nil), keychain: func(service string) (string, error) {
		if service != "seneca-anthropic" {
			return "", provider.ErrKeychainItemNotFound
		}
		return "sk-from-keychain\n", nil
	}, baseURL: server.URL}

	review := &fakeReview{tty: false}
	cfg := reviewConfig{Harness: harnessAnthropic, Model: "claude-sonnet-5-5", Effort: "xhigh"}
	if err := executeReview(providerRuntime(review, archiveTime(24, 20, 0), reviewer), cfg, newArchivePath(t), savedWindow(t)); err != nil {
		t.Fatalf("executeReview returned error: %v", err)
	}

	request := fake.only(t)
	if request.apiKey != "sk-from-keychain" {
		t.Fatalf("expected the Keychain key to be sent, got %q", request.apiKey)
	}
	if !strings.Contains(request.betas, "server-side-fallback-2026-07-01") {
		t.Fatalf("expected the server-side fallback beta, got %q", request.betas)
	}
	body := request.body
	if body["model"] != "claude-sonnet-5-5" {
		t.Fatalf("expected the configured model, got %v", body["model"])
	}
	if body["stream"] != true {
		t.Fatalf("expected a streaming request, got stream=%v", body["stream"])
	}
	if body["fallbacks"] != "default" {
		t.Fatalf("expected fallbacks \"default\", got %v", body["fallbacks"])
	}
	if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != "adaptive" {
		t.Fatalf("expected adaptive thinking, got %v", body["thinking"])
	}
	output, _ := body["output_config"].(map[string]any)
	if output["effort"] != "xhigh" {
		t.Fatalf("expected effort xhigh, got %v", output["effort"])
	}
	format, _ := output["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("expected a json_schema format, got %v", format)
	}
	var want any
	if err := json.Unmarshal([]byte(reviewSchema), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(format["schema"], want) {
		t.Fatalf("expected the review schema verbatim, got %v", format["schema"])
	}

	// Piped output is the same report the harness path prints.
	if !strings.Contains(review.out.String(), savedFocusRecommendation) {
		t.Fatalf("expected the plain report on stdout, got:\n%s", review.out.String())
	}
	if review.decisions != 0 {
		t.Fatal("expected a non-TTY review never to ask for a decision")
	}
}

// Every way the provider can fail ends the review before the decision with an
// error that names the next move, and leaves the archive untouched.
func TestAnthropicFailuresWriteNothing(t *testing.T) {
	validKey := envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey})
	cases := []struct {
		name    string
		fake    *fakeAnthropic
		getenv  func(string) string
		timeout time.Duration
		want    []string
		check   func(t *testing.T, err error)
	}{
		{
			name:   "schema-invalid reply",
			fake:   &fakeAnthropic{text: `{"focus_assessment":null,"struggles":[]}`},
			getenv: validKey,
			want:   []string{"parsing anthropic review response", "does not match the requested schema"},
		},
		{
			name:   "refusal",
			fake:   &fakeAnthropic{stopReason: "refusal", stopDetails: `{"type":"refusal","category":"cyber","explanation":"Declined by policy."}`},
			getenv: validKey,
			want:   []string{"declined this review", "category cyber", "Declined by policy.", "--model", "nothing was written"},
			check: func(t *testing.T, err error) {
				var refusal *provider.RefusalError
				if !errors.As(err, &refusal) {
					t.Fatalf("expected a typed refusal, got %T", err)
				}
			},
		},
		{
			name:   "truncated reply",
			fake:   &fakeAnthropic{text: `{"focus_assessment":`, stopReason: "max_tokens"},
			getenv: validKey,
			want:   []string{"output limit", "--effort", "nothing was written"},
		},
		{
			name:    "timeout",
			fake:    &fakeAnthropic{hang: true},
			getenv:  validKey,
			timeout: 200 * time.Millisecond,
			want:    []string{"anthropic harness exceeded its 200ms time limit", "--timeout", "nothing was written"},
		},
		{
			name:   "missing key",
			fake:   &fakeAnthropic{text: reviewPayload("null")},
			getenv: envWith(nil),
			want:   []string{"no API key", "ANTHROPIC_API_KEY", "security add-generic-password", "seneca-anthropic"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.fake.start(t)
			path := archiveWithFocus(t, 18, "Ship the archive layer.")
			before := readFileString(t, path)

			review := &fakeReview{tty: true, decision: reviewDecision{kind: reviewAcceptFocus, focusText: savedFocusRecommendation}}
			reviewer := providerReviewer{getenv: tc.getenv, keychain: noKeychain, baseURL: server.URL}
			cfg := anthropicConfig()
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
			if strings.Contains(err.Error(), fakeAnthropicKey) {
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

func TestAnthropicRejectedKeyNamesItsSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	t.Cleanup(server.Close)

	reviewer := providerReviewer{getenv: envWith(map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}), keychain: noKeychain, baseURL: server.URL}
	_, err := reviewer.review(anthropicConfig(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "rejected the key from ANTHROPIC_API_KEY") {
		t.Fatalf("expected the rejected key to name its source, got %v", err)
	}
}

func TestResolveKeyPrefersTheEnvironmentAndNeverConfig(t *testing.T) {
	t.Parallel()

	keychainCalls := 0
	keychain := func(string) (string, error) {
		keychainCalls++
		return "from-keychain", nil
	}

	key, label, err := provider.ResolveKey(provider.AnthropicKey, envWith(map[string]string{"ANTHROPIC_API_KEY": " from-env "}), keychain)
	if err != nil || key != "from-env" || label != "ANTHROPIC_API_KEY" || keychainCalls != 0 {
		t.Fatalf("expected the environment key without a Keychain read, got key=%q label=%q err=%v calls=%d", key, label, err, keychainCalls)
	}

	key, label, err = provider.ResolveKey(provider.AnthropicKey, envWith(nil), keychain)
	if err != nil || key != "from-keychain" || label != "Keychain (seneca-anthropic)" {
		t.Fatalf("expected the Keychain key, got key=%q label=%q err=%v", key, label, err)
	}

	_, _, err = provider.ResolveKey(provider.AnthropicKey, envWith(nil), func(string) (string, error) {
		return "", errors.New("user interaction is not allowed")
	})
	if err == nil || !strings.Contains(err.Error(), "seneca-anthropic") || errors.As(err, new(*provider.MissingKeyError)) {
		t.Fatalf("expected a Keychain failure to be reported as one, got %v", err)
	}

	// config.toml has no key field at all: an api_key there is an unknown key
	// the [review] table never reads.
	if _, ok := reflect.TypeOf(reviewFileConfig{}).FieldByNameFunc(func(name string) bool {
		return strings.Contains(strings.ToLower(name), "key")
	}); ok {
		t.Fatal("reviewFileConfig must never carry an API key")
	}
}

func TestResolveReviewConfigEffortAndModelPrecedence(t *testing.T) {
	t.Parallel()

	file := reviewSettings{Harness: harnessAnthropic, Model: "claude-opus-5", Effort: "low"}
	env := envWith(map[string]string{"SENECA_EFFORT": "medium", "SENECA_MODEL": "claude-sonnet-5-5"})

	for _, tc := range []struct {
		name       string
		effortFlag string
		effortSet  bool
		modelFlag  string
		modelSet   bool
		getenv     func(string) string
		file       reviewSettings
		wantEffort string
		wantModel  string
	}{
		{name: "flag beats env beats file", effortFlag: "MAX", effortSet: true, modelFlag: "claude-fable-5-1", modelSet: true, getenv: env, file: file, wantEffort: "max", wantModel: "claude-fable-5-1"},
		{name: "env beats file", getenv: env, file: file, wantEffort: "medium", wantModel: "claude-sonnet-5-5"},
		{name: "file alone", getenv: envWith(nil), file: file, wantEffort: "low", wantModel: "claude-opus-5"},
		{name: "defaults", getenv: envWith(nil), file: reviewSettings{Harness: harnessAnthropic}, wantEffort: "high", wantModel: "claude-opus-5-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := resolveReviewConfig("", false, tc.modelFlag, tc.modelSet, tc.effortFlag, tc.effortSet, defaultReviewTimeout, tc.getenv, tc.file)
			if err != nil {
				t.Fatalf("resolveReviewConfig returned error: %v", err)
			}
			if cfg.Harness != harnessAnthropic || cfg.Effort != tc.wantEffort || cfg.Model != tc.wantModel {
				t.Fatalf("expected anthropic/%s/%s, got %s/%s/%s", tc.wantModel, tc.wantEffort, cfg.Harness, cfg.Model, cfg.Effort)
			}
		})
	}
}

func TestResolveReviewConfigRefusesEffortItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		harness    string
		effortFlag string
		effortSet  bool
		getenv     func(string) string
		file       reviewSettings
		want       string
	}{
		{name: "flag on a CLI harness", harness: "claude", effortFlag: "high", effortSet: true, getenv: envWith(nil), want: "--effort: the claude harness cannot honor an effort setting"},
		{name: "env on a CLI harness", harness: "codex", getenv: envWith(map[string]string{"SENECA_EFFORT": "low"}), want: "SENECA_EFFORT: the codex harness cannot honor"},
		{name: "file on a CLI harness", harness: "copilot", getenv: envWith(nil), file: reviewSettings{Effort: "max"}, want: "[review].effort: the copilot harness cannot honor"},
		{name: "unknown level", harness: "anthropic", effortFlag: "extreme", effortSet: true, getenv: envWith(nil), want: `--effort: invalid effort "extreme": expected low, medium, high, xhigh, max`},
		{name: "blank flag", harness: "anthropic", effortFlag: " ", effortSet: true, getenv: envWith(nil), want: "--effort: invalid effort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveReviewConfig(tc.harness, true, "", false, tc.effortFlag, tc.effortSet, defaultReviewTimeout, tc.getenv, tc.file)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}

	// A CLI harness with no effort anywhere resolves exactly as it always has.
	cfg, err := resolveReviewConfig("claude", true, "", false, "", false, defaultReviewTimeout, envWith(nil), reviewSettings{})
	if err != nil || cfg.Effort != "" || cfg.Model != "" {
		t.Fatalf("expected a CLI harness to carry no effort or default model, got %+v, %v", cfg, err)
	}
}

func TestReviewEffortConfigIsValidatedAtLoadTime(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		want  string
	}{
		{value: "  ", want: "review.effort must not be blank"},
		{value: "huge", want: `review.effort: invalid effort "huge"`},
	} {
		value := tc.value
		_, err := reviewFileConfig{Effort: &value}.reviewSettings()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected %q, got %v", tc.want, err)
		}
	}
	value := " XHigh "
	settings, err := reviewFileConfig{Effort: &value}.reviewSettings()
	if err != nil || settings.Effort != "xhigh" {
		t.Fatalf("expected xhigh, got %+v, %v", settings, err)
	}
}

func TestDoctorReportsTheAnthropicKeySourceNeverTheKey(t *testing.T) {
	h := newDoctorHome(t)
	h.write(t, h.reflections, "")

	for _, tc := range []struct {
		name     string
		env      map[string]string
		keychain provider.KeychainReader
		level    doctorSeverity
		want     string
	}{
		{name: "environment", env: map[string]string{"ANTHROPIC_API_KEY": fakeAnthropicKey}, keychain: noKeychain, level: doctorOK, want: "harness anthropic (from SENECA_HARNESS) uses the API key from ANTHROPIC_API_KEY (not called)"},
		{name: "keychain", keychain: func(string) (string, error) { return fakeAnthropicKey, nil }, level: doctorOK, want: "uses the API key from Keychain (seneca-anthropic)"},
		{name: "missing", keychain: noKeychain, level: doctorWarn, want: "harness anthropic (from SENECA_HARNESS) has no API key"},
		{name: "effort on a CLI harness", env: map[string]string{"SENECA_HARNESS": "claude", "SENECA_EFFORT": "high"}, keychain: noKeychain, level: doctorError, want: "SENECA_EFFORT: the claude harness cannot honor"},
		{name: "invalid env effort", env: map[string]string{"SENECA_EFFORT": "huge"}, keychain: noKeychain, level: doctorError, want: `SENECA_EFFORT: invalid effort "huge"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := h.env()
			values := map[string]string{"SENECA_FILE": h.reflections, "SENECA_HARNESS": "anthropic"}
			for key, value := range tc.env {
				values[key] = value
			}
			env.getenv = envWith(values)
			env.keychain = tc.keychain

			report := diagnose(env)
			requireDoctor(t, report, tc.level, tc.want)
			for _, result := range report.results {
				if strings.Contains(result.detail, fakeAnthropicKey) {
					t.Fatalf("doctor printed the key: %s", result.detail)
				}
			}
		})
	}
}

// The real Keychain reader is never exercised here; this only pins that the
// production wiring uses it, so a test double cannot leak into a release.
func TestProductionWiringReadsTheKeychain(t *testing.T) {
	t.Parallel()
	if reflect.ValueOf(defaultProviderReviewer().keychain).Pointer() != reflect.ValueOf(provider.KeychainReader(provider.SecurityKeychain)).Pointer() {
		t.Fatal("expected the default reviewer to read the macOS Keychain")
	}
	if defaultProviderReviewer().baseURL != "" {
		t.Fatal("expected the default reviewer to use the SDK's own endpoint")
	}
}
