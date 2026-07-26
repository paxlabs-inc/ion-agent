package agent

import "context"

type activityObserverKey struct{}

// WithActivityObserver attaches a lightweight turn watchdog heartbeat. It is
// deliberately independent of user-visible narration: provider deltas and
// real tool lifecycle transitions both count as progress.
func WithActivityObserver(ctx context.Context, touch func()) context.Context {
	if touch == nil {
		return ctx
	}
	return context.WithValue(ctx, activityObserverKey{}, touch)
}

// TouchActivity reports real runtime progress to the owning turn watchdog.
func TouchActivity(ctx context.Context) {
	if touch, ok := ctx.Value(activityObserverKey{}).(func()); ok && touch != nil {
		touch()
	}
}
