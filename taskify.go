// taskify.go — routing chat-originated task work into the playbook state
// machine (issue #237). Three parts on one seam:
//
// Part 1 (task-intent detection): the harness recognizes task-intent phrasing
// ("build me X", "redesign X", "fix this", "create/make X", "coding task: ...",
// "run this as a task") and injects an OFFER into the turn tail. The offer is
// a discussion opener — no scaffold is created, no work starts, until the
// owner explicitly approves (owner lock 2026-08-16: "I don't want Mino to just
// jump straight into a coding task without discussion and approval").
//
// Part 2 (the taskify tool): on approval the model calls taskify, which
// scaffolds a playbook with the harness-guaranteed loop shape (owner lock 2):
// 00-plan → 01-decompose → 02-act → 03-observe → 04-repeat. The model adapts
// the decomposition; the harness guarantees the loop and the checkpoints.
// The scaffold's config.md carries `approval_gate: 01-decompose`: the runner
// pauses the run after 01-decompose and nothing executes before the owner
// approves. Chat-task playbooks are ordinary playbooks in the same engine and
// namespace as scheduled ones (owner lock 3 — no new store).
//
// Part 3 (checkpoint split + cap-resume, absorbs #236): a stage that burns
// its iteration budget without producing its declared outputs gets a
// checkpoint-split offer instead of a hard fail. split_stage accept keeps the
// partial state on disk as the checkpoint and creates a continuation stage
// (02-...-b); the run then resumes mid-task, never from zero. The owner's
// approval reply is intercepted by the harness (approvePendingTaskGate) — the
// model can never approve its own gate (the RUN-006 discipline of approval.go).

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Part 1: task-intent detection + the offer ---

var (
	taskifyVerbRe   = regexp.MustCompile(`(?i)\b(build|redesign|fix|create|make)\b`)
	taskifyPhraseRe = regexp.MustCompile(`(?i)\b(coding task|run this as a task)\b`)
	// "make sure ..." is a checking request, not a task verb (the one real
	// false-positive class on the verb list).
	makeSureRe = regexp.MustCompile(`(?i)\bmake sure\b`)
)

// taskifyOfferText is the owner-approved suggestion wording from the #237
// brief, plus the discussion-opener discipline (lock 1): the offer never
// scaffolds or executes.
const taskifyOfferText = "This looks like a multi-phase task — want me to run it as a structured task? I'll split it into stages (audit → build → verify), save checkpoints between stages, and resume where it left off if interrupted. This is an offer, not a start: do NOT create a playbook or execute any work yet — discuss the approach first, and scaffold only after I approve."

// taskIntentOffer detects task-intent phrasing in the owner's message and
// returns the offer text, or "" when the message is not task-intent. Task
// verbs ALWAYS offer (owner lock: always offer, never auto-start); a trivial
// request ("what time is it") carries no task verb and never matches.
func taskIntentOffer(msg string) string {
	if makeSureRe.MatchString(msg) {
		return ""
	}
	if taskifyPhraseRe.MatchString(msg) || taskifyVerbRe.MatchString(msg) {
		return taskifyOfferText
	}
	return ""
}

// --- Part 2: the taskify scaffold (owner lock 2's five-state shape) ---

// taskifyStageSpec is one scaffold stage for a taskified run.
type taskifyStageSpec struct {
	name    string
	context string
}

// buildTaskifyStages renders the five-state scaffold. The stage contracts are
// bounded by construction (the #238 design-time boundedness check runs over
// them at scaffold time). The task text is embedded in 00-plan's contract so
// the request reaches the run even though run.Request only carries the
// current turn's words ("yes, run it").
func buildTaskifyStages(request, actContract string) []taskifyStageSpec {
	if strings.TrimSpace(actContract) == "" {
		actContract = defaultActContract
	}
	stages := []taskifyStageSpec{
		{
			name: "00-plan",
			context: fmt.Sprintf(`## Process
Write your understanding of the task below into `+"`output/plan.md`"+` exactly once, grounded in the task text verbatim:
- what "done" means for this task (observable acceptance criteria)
- the approach you intend to take
- the main risks and how you will mitigate them
Do NOT start executing the task — this stage produces understanding only, and no work happens before the owner approves the decomposition in stage 01.

## Task

%s

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Plan | `+"`output/plan.md`"+` | Markdown |
`, request),
		},
		{
			name: "01-decompose",
			context: `## Process
Read ` + "`../00-plan/output/plan.md`" + `. Propose the stage split for this task into ` + "`output/decomposition.md`" + ` in a single pass: a numbered list of stages, each with a one-line contract naming its declared outputs and its own bound (name a cap or sample size, never unbounded coverage). The owner approves this decomposition before anything executes — do NOT execute the task itself.

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Previous stage | ` + "`../00-plan/output/plan.md`" + ` | Full file | Understanding to decompose |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Decomposition | ` + "`output/decomposition.md`" + ` | Markdown |
`,
		},
		{
			name: "02-act",
			context: `## Process
Read ` + "`../00-plan/output/plan.md`" + ` and ` + "`../01-decompose/output/decomposition.md`" + ` (the owner-approved decomposition), then execute the task exactly as decomposed, in order, writing each declared output under this stage's ` + "`output/`" + ` directory. Verify your outputs as you go (read back what you wrote before moving on). The stage is bounded: if the decomposition is too large for one bounded stage, accept the harness's checkpoint split offer (split_stage) creating 02-act-b — the run resumes mid-task from the checkpoint, never from zero. Do not loop inside this stage; finish each decomposed step and move on.

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Plan | ` + "`../00-plan/output/plan.md`" + ` | Full file | What "done" means |
| Decomposition | ` + "`../01-decompose/output/decomposition.md`" + ` | Full file | The owner-approved stage split |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Completion report | ` + "`output/completion.md`" + ` | Markdown |
`,
		},
		{
			name: "03-observe",
			context: `## Process
Read ` + "`../02-act/output/completion.md`" + ` and the artifacts it names. Verify the work with verification tools (view_image, read_file, bash) exactly once per artifact: check the artifacts exist, render correctly, and meet the plan's definition of done. Write the verification result to ` + "`output/verification.md`" + `: what was verified, and what gaps remain (name each gap explicitly).

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Previous stage | ` + "`../02-act/output/completion.md`" + ` | Full file | Work to verify |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Verification | ` + "`output/verification.md`" + ` | Markdown |
`,
		},
		{
			name: "04-repeat",
			context: `## Process
Read ` + "`../03-observe/output/verification.md`" + `. If gaps remain: do not loop inside this stage — after this run completes, call split_stage (action=accept, name=act-b, contract naming the partial state as inputs) to create the continuation stage 02-act-b, then call run_playbook again: the run resumes mid-task from the checkpoint. If no gaps remain, write ` + "`output/done.md`" + ` declaring completion once.

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Previous stage | ` + "`../03-observe/output/verification.md`" + ` | Full file | Gaps to close |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Completion | ` + "`output/done.md`" + ` | Markdown |
`,
		},
	}
	// the act stage is the model's adaptation surface (owner lock 2: the
	// model adapts the decomposition; the harness guarantees the loop)
	stages[2].context = actContract
	return stages
}

// defaultActContract is the 02-act contract when the model does not supply
// its own via taskify's act_contract arg.
const defaultActContract = `## Process
Read ` + "`../00-plan/output/plan.md`" + ` and ` + "`../01-decompose/output/decomposition.md`" + ` (the owner-approved decomposition), then execute the task exactly as decomposed, in order, writing each declared output under this stage's ` + "`output/`" + ` directory. Verify your outputs as you go (read back what you wrote before moving on). The stage is bounded: if the decomposition is too large for one bounded stage, accept the harness's checkpoint split offer (split_stage) creating 02-act-b — the run resumes mid-task from the checkpoint, never from zero. Do not loop inside this stage; finish each decomposed step and move on.

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Plan | ` + "`../00-plan/output/plan.md`" + ` | Full file | What "done" means |
| Decomposition | ` + "`../01-decompose/output/decomposition.md`" + ` | Full file | The owner-approved stage split |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Completion report | ` + "`output/completion.md`" + ` | Markdown |
`

// taskifyRootContext is the scaffold's root CONTEXT.md.
func taskifyRootContext(request string) string {
	return fmt.Sprintf(`# Structured chat task

## Task

%s

Taskified by the taskify flow after the owner approved the offer (#237): a
chat-originated multi-phase task running on the playbook engine. The harness
guarantees the plan → decompose → act → observe → repeat loop and the
checkpoints; the model adapts the decomposition.
`, request)
}

// taskifiedPlaybook reports whether the playbook was created by taskify:
// its config carries the approval gate (owner lock 3 — same engine and
// namespace, no separate marker store; the gate's presence IS the marker).
func taskifiedPlaybook(pb *PlaybookWorkspace) bool {
	return pb.Config["approval_gate"] != ""
}

// isApprovalGateStage reports whether the stage is the config's approval gate
// (the stage after which the run pauses for the owner's approval).
func isApprovalGateStage(pb *PlaybookWorkspace, stage WorkspaceStage) bool {
	return pb.Config["approval_gate"] != "" && pb.Config["approval_gate"] == fmt.Sprintf("%02d-%s", stage.Number, stage.Name)
}

// gatePauseReply renders the run's pause message at the approval gate: the
// proposal is on disk, nothing has executed, the owner's approval resumes.
func gatePauseReply(pb *PlaybookWorkspace, run *PlaybookRun, gate WorkspaceStage) string {
	dir := filepath.Join(playbookRunsDir(pb), run.ID, "stages", fmt.Sprintf("%02d-%s", gate.Number, gate.Name), "output")
	return fmt.Sprintf("Run %s paused at the owner-approval gate: stage %02d-%s is complete and its proposal awaits the owner's approval at %s. No task work has executed. Present the proposal to the owner — the run resumes only after the owner explicitly approves it.", run.ID, gate.Number, gate.Name, dir)
}

// makeTaskifyTool creates the taskify tool: scaffold the owner-approved chat
// task as a playbook with the harness-guaranteed five-state loop.
func makeTaskifyTool(core *Core) *Tool {
	return &Tool{
		Name: "taskify",
		Description: "Turn the owner's approved chat request into a structured task: a playbook with the harness-guaranteed plan → decompose → act → observe → repeat loop (stages 00-plan, 01-decompose, 02-act, 03-observe, 04-repeat; an over-scoped act stage splits into 02-act-b via the checkpoint offer). Call ONLY after the owner approved the offer. The scaffold writes understanding first (00-plan), proposes the stage split for the owner's approval (01-decompose — nothing executes before the owner approves, and the run pauses there), then executes (02-act, bounded with checkpoint splits), verifies (03-observe), and repeats on gaps (04-repeat).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":         map[string]any{"type": "string", "description": "Short hyphenated playbook name"},
				"request":      map[string]any{"type": "string", "description": "The owner's task text, verbatim"},
				"act_contract": map[string]any{"type": "string", "description": "Optional override for the 02-act stage contract (the execution contract); must be bounded (no unbounded coverage language)"},
			},
			"required": []string{"name", "request"},
		},
		Fn: func(args map[string]any) string {
			name, _ := args["name"].(string)
			request, _ := args["request"].(string)
			actContract, _ := args["act_contract"].(string)
			if strings.TrimSpace(request) == "" {
				return "Error: request is required (the owner's task text)"
			}
			if strings.TrimSpace(actContract) == "" {
				actContract = defaultActContract
			}
			stages := buildTaskifyStages(request, actContract)
			// #238 integration: scaffold validation runs the design-time
			// boundedness check — an unbounded stage contract is flagged HERE,
			// at scaffold time, not after 50 iterations.
			for _, s := range stages {
				if !researchBounded(s.context) {
					return fmt.Sprintf("Error: scaffold refused — stage %s contract is unbounded (boundedness check): bound the coverage ('most recent 10', 'sample 5', 'exactly once') and retry", s.name)
				}
			}
			stageArgs := make([]any, 0, len(stages))
			for _, s := range stages {
				stageArgs = append(stageArgs, map[string]any{"name": s.name, "context": s.context})
			}
			reply := createManagedPlaybook(core, name, map[string]any{
				"context": taskifyRootContext(request),
				"config":  "status: active\napproval_gate: 01-decompose\n",
				"stages":  stageArgs,
			})
			if strings.HasPrefix(reply, "Error") {
				return reply
			}
			return reply + " Run it now with run_playbook: stages 00-plan and 01-decompose execute, then the run pauses for the owner's approval of the decomposition — present it and wait."
		},
	}
}

// --- Part 3: checkpoint split + cap-resume ---

// splitOfferAtIterations is the >80% stage-budget threshold (of the existing
// 50-iteration cap) at which a capped taskified stage gets the split offer.
const splitOfferAtIterations = int(0.8 * maxStageIterations)

// splitOfferText renders the checkpoint-split offer the harness returns when
// a taskified stage burns its budget without producing its declared outputs.
func splitOfferText(pb *PlaybookWorkspace, run *PlaybookRun, stage WorkspaceStage, iterations int) string {
	dir := filepath.Join(playbookRunsDir(pb), run.ID, "stages", fmt.Sprintf("%02d-%s", stage.Number, stage.Name), "output")
	return fmt.Sprintf("Stage %02d-%s consumed %d of %d iterations without producing its declared outputs. The harness offers a checkpoint split: call split_stage with action=\"accept\", name=\"act-b\", and a continuation contract naming the partial state under %s as inputs — the partial state is kept as the checkpoint and the run continues as a new stage 02-act-b, resuming mid-task, never from zero. Or call split_stage with action=\"decline\" to fail the stage at its budget.", stage.Number, stage.Name, iterations, maxStageIterations, dir)
}

// makeSplitStageTool creates the split_stage tool: accept or decline the
// checkpoint-split offer for a taskified run (or create the continuation a
// completed 04-repeat run proposed for its gaps).
func makeSplitStageTool(core *Core) *Tool {
	return &Tool{
		Name: "split_stage",
		Description: "Accept or decline the checkpoint-split offer for a taskified playbook run (the harness offers it when a stage consumes its iteration budget without producing its declared outputs; 04-repeat also proposes it for gaps). Accept: the stage's partial state is kept as the checkpoint and a continuation stage (02-...-b) is created with the contract you supply — the run resumes mid-task from the checkpoint, never from zero. Decline: the stage fails at its budget and the run fails.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"playbook":  map[string]any{"type": "string", "description": "The playbook name"},
				"action":    map[string]any{"type": "string", "enum": []string{"accept", "decline"}},
				"name":     map[string]any{"type": "string", "description": "The continuation stage name (default: act-b, rendered 02-act-b)"},
				"contract": map[string]any{"type": "string", "description": "The continuation stage CONTEXT.md content; required for accept. Declare the partial state paths as inputs (output/... within the split stage's dir) and name the outputs the continuation produces. Must be bounded."},
			},
			"required": []string{"playbook", "action"},
		},
		Fn: func(args map[string]any) string {
			playbook, _ := args["playbook"].(string)
			action, _ := args["action"].(string)
			stageName, _ := args["name"].(string)
			contract, _ := args["contract"].(string)
			return doSplitStage(core, playbook, action, stageName, contract)
		},
	}
}

func doSplitStage(core *Core, playbook, action, stageName, contract string) string {
	pb, err := loadPlaybookWorkspace(core.Settings.Home, playbook)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	run, err := latestPlaybookRun(pb)
	if err != nil || run == nil {
		return "Error: no run found for this playbook"
	}
	switch action {
	case "decline":
		if run.Status != "awaiting_split" {
			return "Error: no run is awaiting a split decision for this playbook"
		}
		run.Status = "failed"
		if err := savePlaybookRun(pb, run); err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return fmt.Sprintf("Split declined — the over-scoped stage failed at its iteration budget; run %s is failed and cannot resume. Revise the stage contract (manage_playbook update) and start a fresh run.", run.ID)
	case "accept":
		if strings.TrimSpace(contract) == "" {
			return "Error: contract is required for accept (the continuation stage CONTEXT.md, naming the partial state as inputs)"
		}
		if !validPlaybookName(stageName) {
			return "Error: name must use lowercase letters, digits, and single hyphens"
		}
		if run.Status != "awaiting_split" && run.Status != "complete" {
			return "Error: no run is awaiting a split decision for this playbook"
		}
		return acceptSplitStage(core, pb, run, stageName, contract)
	default:
		return "Error: action must be accept or decline"
	}
}

// acceptSplitStage keeps the partial state as the checkpoint and creates the
// continuation stage. For an awaiting_split run the continuation inserts right
// after the over-scoped stage (02-act → 02-act-b → 03-observe); for a
// completed run (the 04-repeat flow) it appends after 04-repeat and reopens
// the run, so the next run_playbook resumes mid-task from the checkpoint.
func acceptSplitStage(core *Core, pb *PlaybookWorkspace, run *PlaybookRun, stageName, contract string) string {
	number, insertAt := 2, len(run.Stages)
	prev := "02-act"
	for i := range run.Stages {
		if run.Stages[i].Name == "act" {
			number = run.Stages[i].Number
		}
		if run.Stages[i].Status == "failed" && run.Status == "awaiting_split" {
			// The over-scoped stage: keep its partial state on disk as the
			// checkpoint and insert the continuation right after it.
			run.Stages[i].Status = "complete"
			run.Stages[i].Error = "checkpoint split: partial state kept on disk"
			insertAt = i + 1
			prev = fmt.Sprintf("%02d-%s", run.Stages[i].Number, run.Stages[i].Name)
		}
	}
	canonical := canonicalManagedStageContext(contract)
	canonical = canonicalManagedStageInputs(canonical, prev)
	// The continuation contract is scaffold too: bound it at creation (the
	// #238 boundedness check), not after 50 iterations.
	if !researchBounded(canonical) {
		return "Error: continuation contract is unbounded (boundedness check): bound the coverage ('most recent 10', 'sample 5', 'exactly once') and retry"
	}
	folder := fmt.Sprintf("%02d-%s", number, stageName)
	stageDir := filepath.Join(pb.Dir, "stages", folder)
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if err := writePlaybookFile(filepath.Join(stageDir, "CONTEXT.md"), canonical); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	// Keep run.Stages in the playbook's stage order: insert right after the
	// checkpoint stage (awaiting_split) or append (completed run).
	after := append([]PlaybookRunStage(nil), run.Stages[insertAt:]...)
	run.Stages = append(run.Stages[:insertAt], PlaybookRunStage{Number: number, Name: stageName, Status: "pending"})
	run.Stages = append(run.Stages, after...)
	run.Status = "running"
	if err := savePlaybookRun(pb, run); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf("Checkpoint accepted: stage %02d-%s created with the continuation contract; the partial state of %s is the checkpoint. Call run_playbook with name=%q to resume mid-task from the checkpoint — never from zero.", number, stageName, prev, pb.Name)
}

// --- The owner's approval of the gate (harness-owned, RUN-006 discipline) ---

// taskGateApprovalRe matches the owner's affirmative reply to a paused gate.
// Conservative: no commas — a qualified "yes, but ..." is not an approval.
var taskGateApprovalRe = regexp.MustCompile(`(?i)^\s*(yes|yep|yeah|y|approved|approve|go ahead|do it|ok|okay|proceed|run it|sounds good|looks good|good to go|go)\b[^,]*$`)

// approvePendingTaskGate handles the owner's approval of a paused task gate.
// Returns the routing block to append to the turn (directing the model to
// resume the run), or "" when there is nothing to approve. The harness owns
// the gate: the model can never approve its own run (approval.go's RUN-006
// discipline), and no scaffold or execution happens before this approval.
func approvePendingTaskGate(home, sessionID, msg string) string {
	if !taskGateApprovalRe.MatchString(msg) {
		return ""
	}
	pb, run := latestAwaitingGateRun(home, sessionID)
	if run == nil {
		return ""
	}
	run.GateApproved = true
	run.Status = "running"
	if err := savePlaybookRun(pb, run); err != nil {
		slog.Error("task gate approval: saving run state failed", "playbook", pb.Name, "run", run.ID, "error", err)
		return ""
	}
	slog.Info("task gate approved by owner", "playbook", pb.Name, "run", run.ID, "session", sessionID)
	return fmt.Sprintf("\nThe owner approved the decomposition for task run %s of playbook %q. Your first action MUST be run_playbook with name=%q. Do NOT do the work yourself first — the playbook's stages perform the task. Run it, then report the result.", run.ID, pb.Name, pb.Name)
}

// latestAwaitingGateRun finds the newest run paused at an approval gate for
// the session (the run the owner's approval refers to).
func latestAwaitingGateRun(home, sessionID string) (*PlaybookWorkspace, *PlaybookRun) {
	var bestPB *PlaybookWorkspace
	var bestRun *PlaybookRun
	for _, name := range ListPlaybooks(home) {
		pb, err := loadPlaybookWorkspace(home, name)
		if err != nil {
			continue
		}
		run, err := latestPlaybookRun(pb)
		if err != nil || run == nil || run.Status != "awaiting_approval" || run.SessionID != sessionID {
			continue
		}
		if bestRun == nil || run.UpdatedAt.After(bestRun.UpdatedAt) {
			bestPB, bestRun = pb, run
		}
	}
	return bestPB, bestRun
}

// previousStage returns the workspace stage preceding the given one in stage
// order — the runner checks it against the approval gate before each stage.
func previousStage(pb *PlaybookWorkspace, stage WorkspaceStage) (WorkspaceStage, bool) {
	for i, s := range pb.Stages {
		if s.Number == stage.Number && s.Name == stage.Name && i > 0 {
			return pb.Stages[i-1], true
		}
	}
	return WorkspaceStage{}, false
}
