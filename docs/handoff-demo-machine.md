# Handoff: Finishing DataStack on the Demo Machine

> **Status: done, 2026-10-09/10.** All five tasks were worked through on the demo machine (Windows 11, Docker Desktop with 12 CPUs and 62 GB, Ollama on an RTX 3060 12 GB). The analyzer answers end to end and scores **20/20** with `qwen2.5-coder` and 15/20 with `qwen3.5:9b` ([analyzer-service.md, Eval Results](microservices/analyzer-service.md#eval-results)); the web UI was driven through every page in a browser; and a full rehearsal ran all three acts and all four chaos scenarios. [demo-runbook.md](demo-runbook.md) has been corrected to match what actually happened. The sections below are kept as the record of what was asked for.
>
> Five things were broken and are now fixed: the `golang:1.26-alpine` builder had drifted to Go 1.26.2 so **no service could build**; the Tracing dashboard's service map was dead (`nodegraph` should be `nodeGraph`); Tempo was being OOMKilled at 640Mi; the restocker's 2-minute loop made OUT_OF_STOCK the dominant failure even with healthy knobs; and three analyzer questions failed on documentation gaps rather than model error. Details are in the final report and the per-file changes.


This is for an AI agent (or a person) continuing the work on the machine that runs the full kind stack. The code is complete, unit-tested and documented. What remains has to be checked live, and the development laptop can't run the stack. Work through the tasks in order. Each task says what "done" means.

## Before you start

- **Read first:** [CLAUDE.md](../CLAUDE.md) for the rules, [demo-runbook.md](demo-runbook.md) for bring-up and the demo, and [analyzer-service.md](microservices/analyzer-service.md) for the analyzer.
- **Rules that matter most here:**
  - Always target the kind cluster explicitly: use the make targets, `kubectl --context kind-datastack` or `helm --kube-context kind-datastack`. The user's kubeconfig also has production clusters.
  - Don't commit; the user commits.
  - Ask the user before doing anything destructive outside the kind cluster.
- **Where things stand:**
  - The six services have runtime chaos knobs (`pkgs/chaos`, README §3.8). The Order Service's reconciler also resolves orders stranded in `PENDING`. Both were verified live on an overloaded laptop, apart from the items listed under Task 2.
  - The Analyzer Service (`services/analyzer`) builds and its unit tests pass, but it has **never answered a question end to end**. Its first live attempts on the laptop ran into CPU-only Ollama (minutes per question). Since then:
    - an overall per-question deadline (`ASK_TIMEOUT`, 240s on kind) sits below the web UI's 280s proxy timeout
    - Ollama replies are capped at 512 tokens (`OLLAMA_MAX_TOKENS`)
  - The web UI (`apps/web`) passes lint and build. Its API routes were checked with curl, but **nobody has looked at it in a browser**.
  - The evaluation set (`make eval`, 20 questions in `services/analyzer/internal/eval/questions.yaml`) has **never been run**.

## Task 1: Bring the stack up

1. Install Ollama and pull `qwen2.5-coder` (and `qwen3.5:9b` for comparison). Work out the `OLLAMA_URL` the pods should use: see runbook §2. On Linux, `host.docker.internal` does not resolve inside kind; use the kind network gateway, with Ollama listening on `0.0.0.0`.
2. Follow runbook §3: `make kind-up infra analytics observability`, then `make deploy-all OLLAMA_URL=<url>`, then `make load RATE=5`.
3. Let it run for about 15 minutes, then go through the runbook §4 pre-flight checklist.

**Done when:**
- every pod is ready, and restarts aren't climbing over 10 minutes
- `curl localhost:8090/status` (after `make forward-cdc-sync`) shows every table syncing and `comments_in_sync: true`
- the in-cluster Ollama probe from runbook §2 returns a version

**If pods restart under load**, check that Docker has the memory and CPUs listed in runbook §1 before changing any limits. If you do change a limit, update the budget table in `.github/skills/local-resources/SKILL.md`.

## Task 2: Verify the analyzer and the observability end to end

1. `make forward-analyzer`, then:
   - `curl localhost:8086/models` should list the pulled models.
   - `curl localhost:8086/schema | jq '.joins | length'` should print 9.
   - Ask a question:
     ```sh
     curl -XPOST localhost:8086/ask -H 'content-type: application/json' \
       -d '{"question":"What is the total revenue for each customer loyalty tier?"}'
     ```
2. Check the analyzer log for `llm warmed up`. The first question after start-up should then be fast.
3. Open the returned `trace_id` in Grafana (`make grafana`, Explore → Tempo). Expect the spans `schema.prompt`, `llm.generate`, `sql.guard`, `sql.explain` and `sql.execute`.
4. Set `latency_ms` to 1000 on inventory, then reset it:
   ```sh
   make forward-inventory
   curl -XPUT localhost:8083/admin/chaos -d '{"latency_ms":1000}'
   ```
   A checkout trace should show the delay on the inventory span (the `chaos.latency_ms` attribute, plus `chaos.latency.start` and `chaos.latency.end` events).
5. Confirm generated SQL can't write: an `INSERT` run as `analyzer_ro` must fail with "Access denied". The analyzer doc says how this was checked.

**Done when:** `/ask` returns rows, the trace shows every step, and the chaos latency is visible in Tempo. If something fails, fix it in the code, add a test, and redeploy with `make deploy-<name>`.

## Task 3: Run the evaluation and tune

1. `make eval`, then `make eval EVAL_ARGS="--eval-model qwen3.5:9b"`. Use `--cases <id>` to re-run single cases.
2. For each failing case, decide where the fault is:
   - **The reference SQL is wrong** (the data is live; references were written against StarRocks 3.3): fix `questions.yaml`.
   - **The model misread a column or table:** improve that column's `comment:` tag or `TableComment()` in the owning service's GORM model. Then run `make catalog` to regenerate and validate `docs/microservices/data-catalog.md`, redeploy that service and the CDC daemon picks the comment up within a minute. This is the preferred fix, because the documentation is the product.
   - **The model breaks a general rule** (dialect, a metric definition, a join): adjust `rules` or `examples` in `services/analyzer/internal/analyzer/prompt.go`. Examples must not overlap the evaluation questions.
3. Re-run until the score stops improving. Keep the prompt compact, because every token costs time on slower hardware.
4. Record the score per model, with notable failures, in the "Eval Results" section of `docs/microservices/analyzer-service.md`. Mention the hardware: GPU or CPU, and the model.

**Done when:** scores for both models are recorded, and any remaining failures are explained.

## Task 4: Check the web UI in a browser

`make forward-web` (http://localhost:3001) alongside `make grafana` (http://localhost:3000).

- **Ask:**
  - The model picker and example chips work.
  - Answers show the SQL, the table, and a bar chart when there is one category column and one number column.
  - Errors show the failed SQL.
  - **View trace** opens the right trace in Grafana. This link has never been tried against a real trace.
- **Chaos:**
  - There are six cards, and every knob changes and resets.
  - Drain stock works.
  - "Reset all chaos" works.
- **Load:**
  - Start, stop and rate changes work, and k6 follows.
- **Grafana links:** all seven dashboards open. The list is hard-coded in `apps/web/src/lib/dashboards.ts`.
- **Projector check:** it should be readable at presentation distance. Fix contrast or size problems in `apps/web`.

**Done when:** every page works, and anything fixed has been redeployed with `make deploy-web`. Run `npm run lint` and `npm run build` in `apps/web` after changes.

## Task 5: Rehearse the demo

Run runbook §5 end to end: all three acts, and every chaos scenario in Act 3. For each scenario, confirm the dashboards and the analyzer show what the runbook promises, within a time that works on stage. Correct the runbook wherever reality differs: knob values, timings, wording of the questions. Prefer analyzer questions that passed the evaluation.

**Done when:** a full rehearsal runs without surprises, and the runbook matches it.

## Known limitations (fine for the demo, don't fix unless asked)

- **Chaos state is in memory, per pod.** A pod restart resets its knobs (README §3.8).
- **Two one-statement windows in the reconciler** (order-service.md, "Known gap"): a crash at exactly the wrong moment can leave stock unreleased or release it twice.
- **The analyzer's read-only grant** is bound to `analytics_db`'s internal id. It is re-applied every minute, so recreating the database heals within a minute.

## Report back

When you finish (or stop), summarise for the user:
- what was verified, with evidence
- what you changed, file by file
- the eval scores
- anything still open

Then run `make lint` and `make test` and include the results.
