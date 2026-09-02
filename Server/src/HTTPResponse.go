package main

import "encoding/json"

// ErrorResponse is the small JSON error envelope retained by auth/permissions.
type ErrorResponse struct {
	Error string `json:"error"`
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"error":"json encode failed"}`)
	}
	return data
}
