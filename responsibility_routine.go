package main

import (
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
