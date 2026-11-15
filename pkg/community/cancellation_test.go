package community

import (
	"context"
	"fmt"
	"testing"
)

func TestErrorPayloadPreservesLocalCancellation(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "request_canceled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
	} {
		t.Run(test.code, func(t *testing.T) {
			api := ErrorPayload(fmt.Errorf("request: %w", test.err))["error"].(*APIError)
			if api.Code != test.code || api.Retryable {
				t.Fatalf("local cancellation reported as an input or retryable failure: %+v", api)
			}
		})
	}
}
