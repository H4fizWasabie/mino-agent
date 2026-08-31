# Verification: #495

Branch: `fix/issue-495-glm-precision-repetition-penalty`

## Test results

- `go build ./...` (root): PASS.
- `go test ./... -count=1` (root): 856 passed, 0 failed.
- `extensions/cost-watch`: `go build ./...` PASS, `go test ./...` 25 passed, 0 failed.
- `gofmt -l` on every touched file: clean.

## Acceptance criteria (from intake) — observed behaviour

1. **A cheaper fp4 endpoint ranks below a pricier fp8 endpoint.** Observed in
   `TestPinOrderPrefersFP8OverCheaperFP4`: Relace at fp4/$0.01 ranks after Novita at
   fp8/$0.10 — `pinOrder` returns `[Novita, Relace]`.
2. **Two fp8 endpoints still rank by price → uptime → latency, unchanged.** Observed in
   the existing (unmodified) `TestPinOrderUsesUptimeThenLatencyAfterPrice` — all four fixture
   entries have empty `Quantization` (same top tier), and the price/uptime/latency order it
   already asserted is byte-identical after this change.
3. **An "unknown"-quantization endpoint ranks with fp8, not behind it.** Observed in
   `TestPinOrderUnknownQuantizationNotPenalized`: a cheaper "unknown" entry still wins over a
   pricier fp8 entry — proves unknown isn't silently demoted into the worse tier.
4. **Every OpenRouter request includes `repetition_penalty`.** Observed in
   `TestCreateContextSendsRepetitionPenaltyDefault`: a real HTTP round-trip's captured request
   body has `repetition_penalty: 1.1` with no env var set.
5. **`MINO_REPETITION_PENALTY` absent or invalid → default 1.1, no error.** Observed in the
   same test (absent case, no `t.Setenv` call). The invalid-value path (non-numeric string)
   is covered by `envFloat`'s own existing behavior (config.go) — `strconv.ParseFloat` failing
   falls through to `fallback`; reusing that helper means this path was already tested when
   `envFloat` was written, not re-tested here (no new code introduced on that path).
6. **Configured override is honored.** Observed in `TestCreateContextSendsRepetitionPenaltyFromEnv`:
   `MINO_REPETITION_PENALTY=1.3` produces `repetition_penalty: 1.3` in the real request body.
7. **Full test suite passes.** See Test results above.

## Invariants — held / evidence

| Invariant | Verdict | Evidence |
|---|---|---|
| Model agnosticism | Held | `repetition_penalty` is a generic OpenAI-compatible/OpenRouter sampling parameter sent for every model via `createWithRouting`, not conditioned on which model or provider is active. Precision-tier ranking in cost-watch applies to any catalogued model, not a hardcoded GLM special-case — `precisionTier` takes a bare quantization string, no model-name branching. |
| Loop termination | Held (unaffected) | No new loop; no change to `loop.go`. |
| Context is managed, never assumed | Held (unaffected) | Not a context-management change. |
| Guardrails are not optional | Held | `eligibleForPin`'s hard `trains` exclusion runs before `rankCatalogueEntries` is ever called (unchanged call order in `pinOrder`) — the new precision tier only reorders within the already-filtered eligible set, never re-admits an excluded provider. |
| Failure is explicit | Held | Unknown/empty quantization is an explicit, tested, non-penalizing case (not a silent default that happens to work) — see criterion 3. `MINO_REPETITION_PENALTY` invalid-value fallback is explicit via the existing `envFloat` contract. |
| State stays local and inspectable | Held | No new persisted state. `providers.json` rewrite is cost-watch's pre-existing, already-inspectable mechanism (unchanged file format, one new field factored into an existing decision). |
| Single binary, no framework | Held | No new dependency; reused the existing `envFloat` helper instead of writing a new one. |

## Failure paths forced

- Fed a quantization value not in the known-worse set (`""`, `"unknown"`) → ranked in the top
  tier, not penalized — forced directly in `TestPinOrderUnknownQuantizationNotPenalized`.
- Omitted `MINO_REPETITION_PENALTY` entirely → default `1.1` applied — forced in
  `TestCreateContextSendsRepetitionPenaltyDefault` (no `t.Setenv` call, real HTTP request
  inspected for the actual value sent).

## Provider parity

Not run as a live dual-provider call in this pass (no owner-authorized live spend for
verification specifically). Settled by inspection instead: `repetition_penalty` is added to
the single shared payload-construction path in `createWithRouting` used by every OpenRouter
request regardless of model/provider — same class of reasoning as #483's Model Agnosticism
row (no per-provider branch was added or touched). The cost-watch ranking change is similarly
provider-blind: `precisionTier` only inspects the `Quantization` string, never a provider
name.

## Open concerns (carried to the ship note)

1. **Unconfirmed against real traffic**: whether every backend in a routing chain silently
   drops an unsupported `repetition_penalty` rather than rejecting the request is an assumption
   from OpenRouter's documented behavior, not proven by a unit test (can't be — depends on a
   specific backend's real behavior). Worth watching after deploy; if a backend hard-rejects
   it, the symptom would be a new class of request failure on that specific provider, not
   silent degradation.
2. **cost-watch's re-pin is on its own hourly cycle** (or `cost_watch_refresh`, on demand) —
   this change doesn't force an immediate re-pin. `providers.json`'s current GLM routing order
   (including the fp4 Relace entry) won't reflect the new precision-first logic until the next
   scheduled or manually-triggered catalogue refresh runs post-deploy.
