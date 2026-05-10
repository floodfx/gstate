# Delayed Transitions

Demonstrates automatic time-based transitions using `After()`. Useful for
timeouts, heartbeats, and debouncing — no manual timer management needed.

## State Diagram

```mermaid
---
title: Timeout
config:
    theme: default
    maxTextSize: 50000
    maxEdges: 500
    fontSize: 16
---
stateDiagram-v2
    state "other_state" as other_state
    state "timeout" as timeout
    state "waiting" as waiting
	[*] --> waiting
	waiting --> other_state: USER_ACTION
	waiting --> timeout: after 100ms
```

## Key Concepts

- **`After(duration)`** triggers a transition after the specified time elapses
- The timer is **automatically cancelled** if the state is exited early (e.g. by `USER_ACTION`)
- Useful for **timeouts**, **heartbeats**, and **debouncing**
- No manual timer management needed — the statechart handles it

## Running

```sh
go run .
```
