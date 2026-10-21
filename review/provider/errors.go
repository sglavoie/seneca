package provider

import "fmt"

// The errors below are what both providers report for a reply that cannot be
// a review. Each says what to do next and that nothing was written.

// RefusalError is a review the model declined. For Anthropic it is a decline
// that survived the server-side fallback; for OpenAI it is a refusal in the
// reply or a reply stopped by the content filter.
type RefusalError struct {
	Model       string
	Category    string
	Explanation string
}

func (e *RefusalError) Error() string {
	detail := "no category given"
	if e.Category != "" {
		detail = "category " + e.Category
	}
	message := fmt.Sprintf("%s declined this review (%s)", e.Model, detail)
	if e.Explanation != "" {
		message += ": " + e.Explanation
	}
	return message + "; rerun it, or choose another model with --model or another harness with --harness (nothing was written)"
}

// TruncatedError is a reply cut off at the output ceiling, which can never be
// a complete review. MaxTokens is zero when the ceiling is the model's own.
type TruncatedError struct {
	Model     string
	MaxTokens int64
}

func (e *TruncatedError) Error() string {
	limit := "its output limit"
	if e.MaxTokens > 0 {
		limit = fmt.Sprintf("its %d-token output limit", e.MaxTokens)
	}
	return fmt.Sprintf("%s stopped at %s before finishing the review; rerun with a lower --effort (nothing was written)", e.Model, limit)
}

// UnexpectedStopError is any other stop reason a single structured-output
// turn should never produce.
type UnexpectedStopError struct {
	Model  string
	Reason string
}

func (e *UnexpectedStopError) Error() string {
	return fmt.Sprintf("%s stopped with %q instead of finishing the review (nothing was written)", e.Model, e.Reason)
}

// describeAPIStatus turns an HTTP failure from either API into the next move:
// a rejected key is reported against the source that supplied it, and a busy
// API asks for a rerun. Anything else keeps the SDK's own message.
func describeAPIStatus(api string, status int, keyLabel string, err error) error {
	switch status {
	case 401, 403:
		return fmt.Errorf("the %s API rejected the key from %s (HTTP %d); replace it there (nothing was written)", api, keyLabel, status)
	case 429, 503, 529:
		return fmt.Errorf("the %s API is rate limited or overloaded (HTTP %d); rerun in a minute (nothing was written)", api, status)
	default:
		return fmt.Errorf("calling the %s API: %w", api, err)
	}
}
