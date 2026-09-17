# Pause or leave

This chapter explains how to pause OpenTacit, disconnect one machine, or remove a
registry. It also lists the data that each action keeps.

## Pause this machine

```
tacit pause
```

Nothing on this machine is observed while a pause is on: no capture, no
suggestions, no record of the turn. It takes effect on your next turn. No
tool is unwired and no setting is lost.

Use it when you pair, demonstrate, or work on something you would rather
nothing looked at.

Asking still works while paused. `tacit ask "what you are working on"` and
the `/tacit:` commands in your AI tool go straight to the playbook and never
pass through the part that a pause switches off.

```
tacit resume
```

Suggestions can reach you again from your next turn.

The pause is a file, `~/.tacit-paused`. You can delete it instead of running
`tacit resume`; the effect is the same.

## Disconnect this machine

```
tacit disconnect
```

This removes the wiring from your AI tools, your member settings, and the
local memory of what you adopted and dismissed.

It reports that it leaves three things in place:

- **The binary.** Remove it with `rm` if you are finished with it.
- **Your model key**, in `~/.tacit-key.env`. It is yours, and it may be
  paying for other things.
- **This machine's member key on the registry**, which stays valid.
  Disconnecting is local. Only an admin can revoke a key, on the Members
  page. Ask for that if the machine is being retired or was lost.

To unwire one tool and keep the rest, name it: `tacit disconnect --harness
codex`.

## Remove a registry

```
tacit disconnect --registry --yes
```

This stops the registry service on this host and retires its settings. Add
`--purge-data` to delete the techniques, data, and models as well.

Disconnecting a member does not delete the organization's data. Removing a
registry affects every member who uses it.
