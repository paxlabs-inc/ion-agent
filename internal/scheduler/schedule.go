// Package scheduler provides encrypted, actor-scoped durable alarms for the
// production Ion runtime.
package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

func nextOnce(delaySeconds int64, fireAt string, now time.Time) (time.Time, error) {
	hasDelay := delaySeconds > 0
	hasAbsolute := strings.TrimSpace(fireAt) != ""
	switch {
	case hasDelay && hasAbsolute:
		return time.Time{}, errors.New("scheduler: once alarm takes delay_seconds or fire_at, not both")
	case hasDelay:
		if delaySeconds > int64((365 * 24 * time.Hour).Seconds()) {
			return time.Time{}, errors.New("scheduler: delay_seconds exceeds one year")
		}
		return now.Add(time.Duration(delaySeconds) * time.Second).UTC(), nil
	case hasAbsolute:
		resolved, err := time.Parse(time.RFC3339, strings.TrimSpace(fireAt))
		if err != nil {
			return time.Time{}, fmt.Errorf("scheduler: fire_at must be RFC3339: %w", err)
		}
		if !resolved.After(now) {
			return time.Time{}, errors.New("scheduler: fire_at must be in the future")
		}
		return resolved.UTC(), nil
	default:
		return time.Time{}, errors.New("scheduler: once alarm requires delay_seconds or fire_at")
	}
}

func nextCron(expression, timezone string, after time.Time) (time.Time, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return time.Time{}, errors.New("scheduler: cron_expr is required")
	}
	if len(expression) > 256 {
		return time.Time{}, errors.New("scheduler: cron_expr is too long")
	}
	compiled, err := cronParser.Parse(expression)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: invalid cron_expr: %w", err)
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	if len(timezone) > 128 {
		return time.Time{}, errors.New("scheduler: timezone is too long")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: invalid IANA timezone %q: %w", timezone, err)
	}
	next := compiled.Next(after.In(location))
	if next.IsZero() {
		return time.Time{}, errors.New("scheduler: cron expression has no future occurrence")
	}
	return next.UTC(), nil
}
