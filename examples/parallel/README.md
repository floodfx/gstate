# Parallel States

This example models a computer input system where a keyboard and mouse operate independently inside a single `active` state. The keyboard tracks whether caps lock is on or off, while the mouse tracks whether a button is clicked or released. Because both regions live inside a `Parallel` state, they run simultaneously — a `CLICK` event only affects the mouse region and a `CAPS_LOCK` event only affects the keyboard region.

## State Diagram

```mermaid
---
config:
  theme: default
  themeVariables:
    fontSize: 16px
---
flowchart TB
    __gstate_start(("●"))
    __gstate_start --> active
    subgraph active ["active"]
        direction LR
        subgraph keyboard ["keyboard"]
            __gstate_initial_6b6579626f617264(("●"))
            __gstate_initial_6b6579626f617264 --> caps_off
            caps_off(["caps_off"])
            caps_on(["caps_on"])
        end
        subgraph mouse ["mouse"]
            __gstate_initial_6d6f757365(("●"))
            __gstate_initial_6d6f757365 --> not_clicked
            clicked(["clicked"])
            not_clicked(["not_clicked"])
        end
    end
    caps_off -->|"CAPS_LOCK"| caps_on
    caps_on -->|"CAPS_LOCK"| caps_off
    clicked -->|"RELEASE"| not_clicked
    not_clicked -->|"CLICK"| clicked
```


<details>
<summary>SCXML</summary>

```xml
<?xml version="1.0" encoding="UTF-8"?>
<scxml xmlns="http://www.w3.org/2005/07/scxml" version="1.0" name="input_system" initial="active">
  <parallel id="active">
    <state id="keyboard" initial="caps_off">
      <state id="caps_off">
        <transition event="CAPS_LOCK" target="caps_on"></transition>
      </state>
      <state id="caps_on">
        <transition event="CAPS_LOCK" target="caps_off"></transition>
      </state>
    </state>
    <state id="mouse" initial="not_clicked">
      <state id="clicked">
        <transition event="RELEASE" target="not_clicked"></transition>
      </state>
      <state id="not_clicked">
        <transition event="CLICK" target="clicked"></transition>
      </state>
    </state>
  </parallel>
</scxml>
```

</details>

## What Happens

When the actor starts, it enters the `active` parallel state, which activates both the `keyboard` and `mouse` regions at the same time. The keyboard begins in `caps_off` and the mouse begins in `not_clicked`.

Sending `CLICK` toggles the mouse region to `clicked` while the keyboard stays in `caps_off` — the event has no effect on a region that doesn't handle it. Sending `CAPS_LOCK` then toggles the keyboard to `caps_on` while the mouse stays in `clicked`. Neither region interferes with the other, and `States()` returns all active leaf states across both regions.

## When To Use This

- **Media players** — playback, volume, and display mode each operate as independent regions, so pressing mute doesn't pause the video.
- **Form validation** — each field runs its own validation region independently, letting one field show an error while others remain valid.
- **Monitoring dashboards** — CPU, memory, and disk health are tracked as separate regions, each with its own normal/warning/critical states.

## Output

```
--- Starting Parallel Actor ---
Active States: [active keyboard mouse not_clicked caps_off]

--- Sending 'CLICK' ---
Active States: [active keyboard mouse clicked caps_off]

--- Sending 'CAPS_LOCK' ---
Active States: [active mouse keyboard caps_on clicked]

--- Conclusion ---
Parallel states avoid the 'state explosion' problem by letting you
model independent behaviors without creating states for every combination.
```

## Running

```bash
go run .
```
