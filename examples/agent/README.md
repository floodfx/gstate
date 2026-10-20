# Agent Workflow

This example models an autonomous agent that investigates a problem (asynchronously), requests human approval, attempts a fix, and verifies the result. The agent can retry on failure — a `RETRY` event loops back to investigating — and an `Always` guard on the fixing state automatically bails out after two fix attempts, preventing infinite loops.

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
    __gstate_start --> investigating
    approving(["approving"])
    done((("done")))
    fixing(["fixing"])
    investigating(["investigating"])
    verifying(["verifying"])
    approving -->|"YES"| fixing
    approving -->|"NO"| done
    fixing -->|"SUCCESS"| verifying
    fixing -->|"RETRY / action"| investigating
    fixing -->|"always [guard] / action"| done
    __gstate_invoke_696e7665737469676174696e67{"__gstate_invoke_696e7665737469676174696e67"}
    investigating --> __gstate_invoke_696e7665737469676174696e67
    __gstate_invoke_696e7665737469676174696e67 -->|"invoke.done"| approving
    __gstate_invoke_696e7665737469676174696e67 -->|"invoke.error"| done
    verifying -->|"FAIL [guard] / action"| fixing
    verifying -->|"FAIL"| done
    verifying -->|"PASS"| done
```


<details>
<summary>SCXML</summary>

```xml
<?xml version="1.0" encoding="UTF-8"?>
<scxml xmlns="http://www.w3.org/2005/07/scxml" version="1.0" name="agent" initial="investigating">
  <state id="approving">
    <transition event="YES" target="fixing"></transition>
    <transition event="NO" target="done"></transition>
  </state>
  <final id="done">
    <onentry>
      <log label="entry action"></log>
    </onentry>
  </final>
  <state id="fixing">
    <transition event="SUCCESS" target="verifying"></transition>
    <transition event="RETRY" target="investigating">
      <assign></assign>
    </transition>
    <transition cond="guard" target="done">
      <assign></assign>
    </transition>
  </state>
  <state id="investigating">
    <transition event="done.invoke.investigating" target="approving"></transition>
    <transition event="error.platform" target="done"></transition>
    <invoke id="investigating"></invoke>
  </state>
  <state id="verifying">
    <transition event="FAIL" cond="guard" target="fixing">
      <assign></assign>
    </transition>
    <transition event="FAIL" target="done"></transition>
    <transition event="PASS" target="done"></transition>
  </state>
</scxml>
```

</details>

## What Happens

The agent starts in **investigating**, which fires an async `Invoke` to run diagnostics. When the invocation completes, the `done.invoke` event moves the agent to **approving**. A simulated controller sends `YES`, advancing to **fixing**.

On the first fix attempt the controller sends `RETRY`, which loops back to **investigating** for another round of diagnostics. The agent goes through approving a second time, gets another `YES`, and lands in **fixing** again.

This time the controller sends `SUCCESS`, moving to **verifying**. The controller sends `PASS`, and the agent transitions to **done** — a final state whose entry action prints a completion message.

If the agent ever reached two fix attempts without success, the `Always` guard (`maxAttempts`) on fixing would fire automatically and send it straight to **done**, bypassing any further retries.

## When To Use This

- **CI/CD pipelines** — build → test → deploy with automatic retry on transient failures and a hard cap on attempts.
- **Customer support bots** — triage → escalate → resolve, with human-approval gates between stages.
- **Automated remediation** — detect → diagnose → fix → verify, with retry budgets so the system doesn't loop forever.

## Output

```
--- Starting Agent Actor ---
-> [investigating] Running diagnostics...
Current State: investigating
Current State: investigating
Current State: approving
Action: Sending YES
Current State: fixing
Action: Sending RETRY
-> [fixing] Retry attempt 1
-> [investigating] Running diagnostics...
Current State: investigating
Current State: investigating
Current State: approving
Action: Sending YES
Current State: fixing
Action: Sending SUCCESS
Current State: verifying
Action: Sending PASS
-> [done] Agent workflow complete.
Current State: done

Simulation finished.
```

## Running

```bash
go run .
```
