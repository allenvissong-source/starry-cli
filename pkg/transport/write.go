package transport

import (
	"encoding/json"
	"errors"
)

// WriteResult preserves the response metadata. Ambiguous upstream failures must
// never be treated as an ordinary retry with a new key.
func WriteResult(response Response, err error) (map[string]any, error) {
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status >= 500 {
			return nil, &APIError{Status: 503, Code: "outcome_unknown", Message: "Preserve original requestKey and payload; query the operation before retrying", Retryable: false}
		}
		return nil, err
	}
	var result any
	if len(response.Data) != 0 {
		if err := json.Unmarshal(response.Data, &result); err != nil {
			return nil, &APIError{Status: 503, Code: "outcome_unknown", Message: "Query the original operation", Retryable: false}
		}
	}
	return map[string]any{"data": result, "httpStatus": response.Status,
		"requestId": response.Header.Get("X-Request-ID"), "etag": response.Header.Get("ETag")}, nil
}
