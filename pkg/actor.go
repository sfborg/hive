package hive

import "context"

type actorKey struct{}

// WithActor returns a context carrying the given actor string. Mutations run
// through *Tx will write this actor to col__modified_by (and other actor
// columns like col__scrutinizer_id where applicable) on every affected row.
//
// In v0 the actor is typically the local username. Post-ORCID-SSO the actor
// will be the ORCID iD, injected by auth middleware. The core package does
// not care where the actor comes from.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFromContext returns the actor stored in ctx, or empty string if none
// was set. An empty actor is legal — some flows (imports, tests) run without
// one — but callers that require attribution should check.
func ActorFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorKey{}).(string); ok {
		return v
	}
	return ""
}
