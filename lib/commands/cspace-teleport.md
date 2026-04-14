---
description: Teleport this Claude Code session to a different cspace container, preserving the conversation and git state.
argument-hint: <target-instance>
---

Teleport this session to the cspace instance named `$ARGUMENTS`.

Run exactly this command and do nothing else:

```
bash /opt/cspace/lib/scripts/teleport-prepare.sh $ARGUMENTS
```

If the script succeeds, it will print a final `Teleport complete` line with
instructions for the user to reconnect on the host. After the script
completes, end your response — do not continue working on any prior task,
because the conversation will now continue in the target container.

If the script fails, surface its full stderr output so the user can
diagnose. Do not attempt to retry or work around the failure.
