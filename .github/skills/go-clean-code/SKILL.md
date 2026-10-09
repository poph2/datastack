---
name: go-clean-code
description: Use when writing or reviewing Go code in DataStack (services/*, any .go file). Keeps code simple, readable, and consistent for human reviewers rather than optimized for cleverness.
argument-hint: Go file or package being written or reviewed
---

# Go Clean Code (DataStack)

DataStack's Go code is read far more than it's written — by teammates, and by future you. Optimize for a human skimming the file top to bottom, not for showing off Go tricks.

## Core Rules

1. **Small functions, one job each.** If a function needs a comment to explain "and then it also...", split it.
2. **Name things for what they mean, not how they're implemented.** `dispatchCreateCustomer`, not `handleMsg1`.
3. **Return errors, don't panic.** Panics are reserved for truly unrecoverable startup failures (e.g. a required env var is missing at boot).
4. **No naked magic numbers/strings.** Give them a named constant with a short comment if the meaning isn't obvious from the name.
5. **Comments explain _why_, not _what_.** Don't restate the line below it in English. A comment earns its place only when the code can't say it on its own (a tradeoff, a non-obvious constraint, a "this looks wrong but isn't" note).
6. **Prefer flat control flow.** Return early on error/invalid input instead of nesting the happy path inside `if`s.
7. **Keep exported surface small.** Only export what other packages actually need. Unexported helpers stay unexported.
8. **One concept per file where practical.** A `model.go`, `service.go`, `commands.go` split is easier to navigate than one 400-line file.
9. **Struct fields and function signatures should read like a sentence.** Avoid boolean parameters with no context at the call site (`New(true, false)`); use named fields or small option structs instead.
10. **Don't add abstraction for a single caller.** Interfaces, generics, and factory functions earn their place only when there's a second real implementation or a genuine test-seam need.

## Formatting And Tooling

- Always run `gofmt -w .` (or rely on `go vet`/editor format-on-save) before considering a change done.
- Run `go build ./...` and `go vet ./...` for the module you touched.
- Keep import groups: stdlib, then third-party, then internal packages, separated by a blank line (`goimports` does this automatically).

## Package Shape

- One package per bounded concept (e.g. `customer`, `inventory`, `order`, `cdc`), not one giant `models` or `utils` package.
- A package's `New(...)` constructor should return a ready-to-use value with all required dependencies passed in explicitly — no hidden global state, no `init()` magic for anything business-logic related.
- Concurrency primitives (channels, mutexes) are owned by the package that needs them and are never exported as raw fields; expose methods instead.

## Error Handling

- Wrap errors with context using `fmt.Errorf("doing X: %w", err)` so failures are traceable without a stack trace library.
- Don't swallow errors silently. If an error genuinely can't be handled (e.g. a background goroutine), log it with enough context to debug, don't just `_ = err`.

## Review Checklist

Before finishing a Go change, check:

- Would a new engineer understand this file's purpose from its name and top-level declarations alone?
- Is there any function longer than ~40 lines that could be split into named steps?
- Are there comments that just repeat the code? Delete them.
- Does every exported identifier have a doc comment starting with its name?
- Did you run `gofmt`, `go build ./...`, and `go vet ./...`?
