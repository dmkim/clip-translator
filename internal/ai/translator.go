package ai

import (
	"context"
	"time"
)

type Translator interface {
	Generate(ctx context.Context, systemPrompt, input string) (string, error)
	Timeout() time.Duration
}
