package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	schemaflux "github.com/monstercameron/schemaflux/v2"
)

// StatusError is a non-200 answer from the provider. Its text is the one the
// package always produced ("llm: provider returned 429: …"); it is a type now so
// the status can be classified for SchemaFlux v2 (see sfError) instead of being
// parsed back out of a string.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("llm: provider returned %d: %s", e.Code, e.Message)
}

// sfError gives an error from the audited call the Kind SchemaFlux v2 acts on.
//
// # Why this exists, and what it changed
//
// v2 retries a provider error it cannot classify as ErrUnavailable — "any other
// error is taken to be the provider being unavailable" — which is retryable. v1
// had its own, narrower opinion. Without this, a missing key, a spent budget, a
// truncated answer and a refusal would each be sent again up to the provider's
// RetryPolicy: three requests (two of them billed) for answers that were never
// going to change.
//
// The original error is kept as Err, so every caller matching this package's
// sentinels with errors.Is (ErrTruncated, ErrNotConfigured, ErrOverBudget, …)
// still matches through the wrapper.
func sfError(err error) error {
	if err == nil {
		return nil
	}
	var already *schemaflux.Error
	if errors.As(err, &already) {
		return err
	}
	kind := schemaflux.ErrUnavailable
	var status *StatusError
	switch {
	case errors.Is(err, context.Canceled):
		kind = schemaflux.ErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		kind = schemaflux.ErrTimeout
	case errors.Is(err, ErrNotConfigured):
		kind = schemaflux.ErrConfiguration
	case errors.Is(err, ErrOverBudget):
		kind = schemaflux.ErrBudgetExceeded
	case errors.Is(err, ErrTruncated):
		kind = schemaflux.ErrTruncated
	case errors.Is(err, ErrRefused):
		kind = schemaflux.ErrRefused
	case errors.Is(err, ErrTypedWebSearch):
		kind = schemaflux.ErrUnsupported
	case errors.Is(err, ErrBusy):
		kind = schemaflux.ErrBusy
	case errors.Is(err, ErrCircuitOpen):
		// Retrying an open circuit is the breaker answering again, instantly,
		// with the same no. Not retryable.
		return &schemaflux.Error{Kind: schemaflux.ErrBusy, Message: "the provider circuit is open", Err: err}
	case errors.As(err, &status):
		switch {
		case status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden:
			kind = schemaflux.ErrUnauthorized
		case status.Code == http.StatusTooManyRequests:
			kind = schemaflux.ErrRateLimited
		case status.Code >= 500:
			kind = schemaflux.ErrUnavailable
		default:
			kind = schemaflux.ErrInvalidRequest
		}
	}
	return &schemaflux.Error{Kind: kind, Message: "articleflux provider", Err: err}
}

// sfUsage reports the Responses API's token counts in SchemaFlux v2's shape.
func sfUsage(u Usage) schemaflux.Usage {
	return schemaflux.Usage{
		Input:  int(u.InputTokens),
		Output: int(u.OutputTokens),
		Total:  int(u.InputTokens + u.OutputTokens),
	}
}
