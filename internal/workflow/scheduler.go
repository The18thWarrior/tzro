package workflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"tzro/internal/memory"
)

// ParseCronNext evaluates a standard 5-field cron expression and returns the next execution time.
func ParseCronNext(expr string, from time.Time) time.Time {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		// Fallback to 5 minutes from now if invalid
		return from.Add(5 * time.Minute)
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return from.Add(5 * time.Minute)
	}

	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return from.Add(5 * time.Minute)
	}

	daysOfMonth, err := parseField(fields[2], 1, 31)
	if err != nil {
		return from.Add(5 * time.Minute)
	}

	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return from.Add(5 * time.Minute)
	}

	daysOfWeek, err := parseDayOfWeekField(fields[4])
	if err != nil {
		return from.Add(5 * time.Minute)
	}

	// Truncate input time to the minute and add 1 minute to find the next future run time
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(5, 0, 0) // Max 5 years forward to prevent infinite loops

	for t.Before(limit) {
		if !months[int(t.Month())] {
			// Skip to start of next month
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}

		// In standard cron, if both day-of-month and day-of-week are restricted (not "*"),
		// then if EITHER matches, it is allowed. Otherwise, BOTH must match.
		domRestricted := fields[2] != "*"
		dowRestricted := fields[4] != "*"

		domMatch := daysOfMonth[t.Day()]
		dowMatch := daysOfWeek[int(t.Weekday())]

		var dayMatch bool
		if domRestricted && dowRestricted {
			dayMatch = domMatch || dowMatch
		} else {
			dayMatch = domMatch && dowMatch
		}

		if !dayMatch {
			// Skip to start of next day
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}

		if !hours[t.Hour()] {
			// Skip to start of next hour
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}

		if !minutes[t.Minute()] {
			// Skip to next minute
			t = t.Add(time.Minute)
			continue
		}

		return t
	}

	return time.Time{}
}

func parseField(field string, min, max int) (map[int]bool, error) {
	allowed := make(map[int]bool)
	if field == "*" {
		for i := min; i <= max; i++ {
			allowed[i] = true
		}
		return allowed, nil
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty part")
		}

		step := 1
		subpart := part
		if idx := strings.Index(part, "/"); idx != -1 {
			stepStr := part[idx+1:]
			var err error
			step, err = strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step value: %s", stepStr)
			}
			subpart = part[:idx]
		}

		if subpart == "*" {
			for i := min; i <= max; i += step {
				allowed[i] = true
			}
		} else if idx := strings.Index(subpart, "-"); idx != -1 {
			startStr := subpart[:idx]
			endStr := subpart[idx+1:]
			start, err1 := strconv.Atoi(startStr)
			end, err2 := strconv.Atoi(endStr)
			if err1 != nil || err2 != nil || start < min || end > max || start > end {
				return nil, fmt.Errorf("invalid range: %s", subpart)
			}
			for i := start; i <= end; i += step {
				allowed[i] = true
			}
		} else {
			val, err := strconv.Atoi(subpart)
			if err != nil || val < min || val > max {
				return nil, fmt.Errorf("invalid value: %s", subpart)
			}
			for i := val; i <= max; i += step {
				allowed[i] = true
				if step == 1 {
					break
				}
			}
		}
	}

	return allowed, nil
}

func parseDayOfWeekField(field string) (map[int]bool, error) {
	allowed, err := parseField(field, 0, 7)
	if err != nil {
		return nil, err
	}
	// In cron, 0 and 7 are both Sunday.
	if allowed[7] {
		allowed[0] = true
		delete(allowed, 7)
	}
	return allowed, nil
}

// GlobalScheduler manages the cron executions
type GlobalScheduler struct {
	ctx    context.Context
	cancel context.CancelFunc
}

var Scheduler = &GlobalScheduler{}

// Start starts the background scheduler loop.
func (s *GlobalScheduler) Start(parentCtx context.Context) {
	s.ctx, s.cancel = context.WithCancel(parentCtx)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		fmt.Println("[Scheduler] Background workflow cron loop started.")

		for {
			select {
			case <-s.ctx.Done():
				fmt.Println("[Scheduler] Background workflow cron loop stopped.")
				return
			case <-ticker.C:
				s.tick()
			}
		}
	}()
}

// Stop stops the scheduler loop.
func (s *GlobalScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *GlobalScheduler) tick() {
	workflows, err := memory.DB.GetWorkflows()
	if err != nil {
		fmt.Printf("[Scheduler Error] Failed to scan workflows: %v\n", err)
		return
	}

	now := time.Now()
	for _, wf := range workflows {
		if wf.Status != "active" {
			continue
		}

		if wf.TriggerType != "cron" {
			continue
		}

		// Calculate NextRunAt if it is unset
		if wf.NextRunAt == 0 {
			next := ParseCronNext(wf.TriggerConfig, now)
			if !next.IsZero() {
				_ = memory.DB.UpdateWorkflowNextRun(wf.ID, next.Unix())
				fmt.Printf("[Scheduler] Set initial next run time for workflow %s: %s\n", wf.Name, next.Format(time.RFC3339))
			}
			continue
		}

		// Fire workflow execution if NextRunAt is in the past or now
		if wf.NextRunAt <= now.Unix() {
			// Update next run time first to prevent double triggers
			next := ParseCronNext(wf.TriggerConfig, now)
			if !next.IsZero() {
				_ = memory.DB.UpdateWorkflowNextRun(wf.ID, next.Unix())
			} else {
				// Paused or finished if no next run
				_ = memory.DB.ToggleWorkflow(wf.ID, "paused")
			}

			fmt.Printf("[Scheduler] Triggering scheduled workflow: %s (%s)\n", wf.Name, wf.ID)
			go func(wfID string) {
				execCtx := context.Background()
				if err := ExecuteWorkflow(execCtx, wfID); err != nil {
					fmt.Printf("[Scheduler Error] Execution failed for workflow %s: %v\n", wfID, err)
				}
			}(wf.ID)
		}
	}
}
