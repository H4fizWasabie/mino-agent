package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *ResponsibilityStore) startRoutine(schedule PlaybookSchedule, at time.Time) error {
	_, err := s.Record(ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name,
		Type:             "started",
		Status:           "working",
		Summary:          "Scheduled run started.",
		NextAction:       "Complete scheduled playbook",
		NextOwner:        "mino",
		Verification:     "Declared playbook outputs must exist before verification.",
		At:               at,
	})
	return err
}

func (s *ResponsibilityStore) startOneOff(id, name, request, sessionID string, at time.Time) error {
	title := "Run " + name + " once"
	outcome := strings.TrimSpace(request)
	if outcome == "" {
		outcome = title
	}
	_, err := s.Record(ResponsibilityEvent{
		ResponsibilityID: id,
		Type:             "accepted",
		Kind:             "one_off",
		Title:            title,
		Outcome:          outcome,
		Owner:            "mino",
		Status:           "working",
		NextAction:       "Complete playbook",
		NextOwner:        "mino",
		SourceKind:       "playbook",
		SourceRef:        id,
		Verification:     "Declared playbook outputs must exist before verification.",
		Summary:          "Accepted one-off playbook run.",
		Evidence:         "session:" + sessionID,
		At:               at,
	})
	return err
}

func (s *ResponsibilityStore) finishOneOff(home, id, sessionID string, result *PlaybookResult, runErr error, at time.Time) error {
	event := ResponsibilityEvent{
		ResponsibilityID: id,
		Type:             "blocked",
		Status:           "blocked",
		Summary:          "One-off playbook did not return a result.",
		NextAction:       "Inspect the playbook run",
		NextOwner:        "mino",
		Evidence:         "session:" + sessionID,
		At:               at,
	}
	if runErr != nil {
		event.Summary = "One-off playbook failed before producing a result."
		event.Evidence += "\nerror:" + runErr.Error()
	} else if result != nil {
		event.Summary = strings.TrimSpace(result.Reply)
		if event.Summary == "" {
			event.Summary = fmt.Sprintf("One-off playbook ended %s.", result.Status)
		}
		var verified int
		event.Evidence, verified = routineEvidence(home, sessionID, result.Outputs)
		if result.Status == "complete" && verified > 0 {
			event.Type = "completed"
			event.Status = "verified"
			event.Summary = fmt.Sprintf("One-off playbook completed with %d verified %s.", verified, plural(verified, "output", "outputs"))
			event.NextAction = "Closed"
		} else if result.Status == "blocked" {
			event.Summary = "One-off playbook needs owner input. " + event.Summary
		} else if result.Status == "complete" {
			event.Summary = "One-off playbook completed without a readable output."
		}
	}
	_, err := s.Record(event)
	return err
}

type playbookResponsibilityRunner func(context.Context, *Core, string, string, string, Observer) (*PlaybookResult, error)

func runPlaybookWithResponsibility(ctx context.Context, core *Core, name, request, sessionID string, run playbookResponsibilityRunner, at time.Time) (string, error) {
	if core.Responsibilities == nil {
		result, err := run(ctx, core, name, request, sessionID, nil)
		if err != nil {
			return "", err
		}
		return formatPlaybookResult(result), nil
	}
	id := fmt.Sprintf("one-off:%s:%s:%d", name, sessionID, at.UnixNano())
	if err := core.Responsibilities.startOneOff(id, name, request, sessionID, at); err != nil {
		return "", err
	}
	result, runErr := run(ctx, core, name, request, sessionID, nil)
	if err := core.Responsibilities.finishOneOff(core.Settings.Home, id, sessionID, result, runErr, time.Now().UTC()); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	return formatPlaybookResult(result), nil
}

func (s *ResponsibilityStore) finishRoutine(
	home, sessionID string,
	schedule PlaybookSchedule,
	result *PlaybookResult,
	runErr error,
	at time.Time,
) error {
	event := ResponsibilityEvent{
		ResponsibilityID: "routine:" + schedule.Name,
		Type:             "blocked",
		Status:           "blocked",
		Summary:          "Scheduled run did not return a result.",
		NextAction:       "Inspect the scheduled run",
		NextOwner:        "mino",
		Evidence:         "session:" + sessionID,
		LastRunAt:        &at,
		At:               at,
	}
	if due, err := nextRoutineRun(schedule, at); err == nil {
		event.DueAt = &due
	}
	if runErr != nil {
		event.Summary = "Scheduled run failed before producing a result."
		event.Evidence += "\nerror:" + runErr.Error()
	} else if result != nil {
		event.Summary = strings.TrimSpace(result.Reply)
		if event.Summary == "" {
			event.Summary = fmt.Sprintf("Scheduled run ended %s.", result.Status)
		}
		var verified int
		event.Evidence, verified = routineEvidence(home, sessionID, result.Outputs)
		switch result.Status {
		case "complete":
			if verified > 0 {
				event.Type = "completed"
				event.Status = "verified"
				event.Summary = fmt.Sprintf("Scheduled run completed with %d verified %s.",
					verified, plural(verified, "output", "outputs"))
				event.NextAction = "Wait for next scheduled run"
			} else {
				event.Summary = "Scheduled run completed without a readable output."
			}
		case "blocked":
			event.Summary = "Scheduled run needs owner input. " + event.Summary
		}
	}
	_, err := s.Record(event)
	return err
}

func routineEvidence(home, sessionID string, outputs []string) (string, int) {
	lines := []string{"session:" + sessionID}
	verified := 0
	for _, output := range outputs {
		info, err := os.Stat(output)
		if err != nil || info.IsDir() {
			continue
		}
		path, err := filepath.Rel(home, output)
		if err != nil || strings.HasPrefix(path, "..") {
			continue
		}
		lines = append(lines, fmt.Sprintf("artifact:%s (%d bytes)", filepath.ToSlash(path), info.Size()))
		verified++
	}
	return strings.Join(lines, "\n"), verified
}

func nextRoutineRun(schedule PlaybookSchedule, after time.Time) (time.Time, error) {
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	return nextScheduledRun(schedule.Time, location, after.Add(time.Minute))
}
