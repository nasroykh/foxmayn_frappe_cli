package llmtest

import (
	"context"
	"fmt"
)

// Images is an llm.ImageResolver over a map of attachment id to bytes.
type Images map[string][]byte

// ImageData returns the bytes stored under id.
func (m Images) ImageData(_ context.Context, id string) ([]byte, error) {
	b, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("attachment %s not found", id)
	}
	return b, nil
}
