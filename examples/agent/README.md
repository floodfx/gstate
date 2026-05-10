# Agent Workflow

This example models an autonomous agent with investigation, approval, fix/retry,
and verification phases. It demonstrates how to combine `Invoke()`, guards,
`Always()` transitions, and retry logic in a single statechart.

## State Diagram

```mermaid
---
title: Agent Workflow
config:
    theme: default
    maxTextSize: 50000
    maxEdges: 500
    fontSize: 16
---
stateDiagram-v2
    state "approving" as approving
    state "done" as done
    state "fixing" as fixing
    state "investigating" as investigating
    state "verifying" as verifying
	done --> [*]
	[*] --> investigating
	approving --> fixing: YES
	approving --> done: NO
	fixing --> verifying: SUCCESS
	fixing --> investigating: RETRY
	fixing --> done: [maxAttempts]
	investigating --> approving: done.invoke
	investigating --> done: error
	verifying --> fixing: FAIL [canRetry]
	verifying --> done: FAIL
	verifying --> done: PASS
```

## Key Concepts

- **`Invoke()`** for async investigation — the `investigating` state delegates to an invoked service and transitions on `done.invoke` or `error`
- **`Always()` with guard** for automatic bail-out at max attempts (`[maxAttempts]`)
- **Guarded transitions** (`[canRetry]`) for conditional retry logic
- **Multiple transitions on the same event** — `FAIL` with and without a guard on `verifying`
- **Final state** (`done`) for workflow completion

## Running

```bash
go run .
```
