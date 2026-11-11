package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The refusals of a decision on an effect.
const (
	CodeEffectDecided = "effect_decided"
	CodeCannotRepeat  = "cannot_repeat"
)

// EffectList is what a list of effects found.
type EffectList struct {
	Effects []ledger.UnknownEffect `json:"effects"`
}

// DecideEffect is a person's word on an effect nothing can observe.
type DecideEffect struct {
	Outcome ledger.EffectOutcome `json:"outcome"`
	Reason  string               `json:"reason,omitempty"`
}

// Effects lists every effect a person heard of, oldest first.
func (c *ProjectClient) Effects(ctx context.Context) ([]ledger.UnknownEffect, error) {
	var out EffectList
	err := c.do(ctx, http.MethodGet, "/v1/effects", nil, &out)
	return out.Effects, err
}

// Effect reads one.
func (c *ProjectClient) Effect(ctx context.Context, id string) (ledger.UnknownEffect, error) {
	var out ledger.UnknownEffect
	err := c.do(ctx, http.MethodGet, "/v1/effects/"+url.PathEscape(id), nil, &out)
	return out, err
}

// DecideEffect carries a person's word.
func (c *ProjectClient) DecideEffect(ctx context.Context, id string, in DecideEffect) (ledger.UnknownEffect, error) {
	var out ledger.UnknownEffect
	err := c.do(ctx, http.MethodPost, "/v1/effects/"+url.PathEscape(id)+"/decide", in, &out)
	return out, err
}
