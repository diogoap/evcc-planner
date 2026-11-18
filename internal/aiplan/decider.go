package aiplan

import (
	"context"
	"encoding/json"

	"github.com/diogoap/evcc-planner/internal/openrouter"
)

// Request is one decision to make: the model, the fixed system prompt, the
// filled-in prompt, and its data (for the stand-in).
type Request struct {
	Model  string
	System string
	User   string
	Input  Input
}

// Decider answers a request with the decision as JSON.
type Decider interface {
	Name() string // openrouter or stub
	Decide(ctx context.Context, r Request) (string, openrouter.Usage, error)
}

// maxOutputTokens caps the answer (a week's decision is about 1,500 tokens).
const maxOutputTokens = 8000

// OpenRouter asks a model through OpenRouter with the strict schema.
type OpenRouter struct{ Client *openrouter.Client }

// Name identifies the decider.
func (*OpenRouter) Name() string { return "openrouter" }

// Decide sends the request.
func (o *OpenRouter) Decide(ctx context.Context, r Request) (string, openrouter.Usage, error) {
	return o.Client.Complete(ctx, openrouter.Request{
		Model: r.Model, System: r.System, User: r.User, SchemaName: "charging_plan", Schema: Schema(),
		MaxTokens: maxOutputTokens, Where: "Settings → AI",
	})
}

// Stub repeats the automatic rules (see StubDecision).
type Stub struct{}

// Name identifies the decider.
func (Stub) Name() string { return "stub" }

// Decide answers from the request's data.
func (Stub) Decide(ctx context.Context, r Request) (string, openrouter.Usage, error) {
	data, err := json.Marshal(StubDecision(r.Input))
	return string(data), openrouter.Usage{Model: "stub"}, err
}
