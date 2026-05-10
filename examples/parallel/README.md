# Parallel States

This example demonstrates **orthogonal (parallel) regions** in a statechart.
An `active` state contains two independent regions — `keyboard` and `mouse` —
that are both active simultaneously. Each region transitions independently,
so a `CLICK` event only affects the mouse region while the keyboard region
remains unchanged. This avoids the combinatorial explosion of states that
would result from modeling every combination explicitly.

## Statechart

```mermaid
---
title: Input System
config:
    theme: default
    maxTextSize: 50000
    maxEdges: 500
    fontSize: 16
---
stateDiagram-v2
    state active {
        state active_fork <<fork>>
        state active_join <<join>>
        state keyboard {
            [*] --> caps_off
            state "caps_off" as caps_off
            state "caps_on" as caps_on
        }
        state mouse {
            [*] --> not_clicked
            state "clicked" as clicked
            state "not_clicked" as not_clicked
        }
    }
	active_fork --> keyboard
	keyboard --> active_join
	active_fork --> mouse
	mouse --> active_join
	[*] --> active
	caps_off --> caps_on: CAPS_LOCK
	caps_on --> caps_off: CAPS_LOCK
	clicked --> not_clicked: RELEASE
	not_clicked --> clicked: CLICK
```

## Key Concepts

- **`Type(Parallel)`** makes all children active simultaneously
- Each region transitions independently — `CLICK` only affects the mouse region
- **`States()`** returns multiple leaf states (one per region)
- Avoids combinatorial explosion of states (2 × 2 = 4 explicit states reduced to 2 + 2)

## Running

```bash
go run .
```
