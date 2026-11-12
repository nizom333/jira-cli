# Issue tracker: beads (bd)

Issues and specs for this repo live in beads (`bd`, v0.59, Dolt backend in `.beads/`).
Always pass `--json` when reading output programmatically.

## Mapping

| Skill concept        | beads                                                                     |
| -------------------- | ------------------------------------------------------------------------- |
| Spec                 | an `epic` issue; the spec text is its `--description`                     |
| Ticket               | a child issue: `bd create "<title>" -t task\|bug\|feature --parent <epic-id> --description=...` |
| Blocked by           | `bd dep add <ticket> <blocker>`; inspect with `bd dep tree <id>` / `bd graph` |
| Frontier             | `bd ready --json` (open, unblocked, unclaimed)                            |
| Claim                | `bd update <id> --claim --json`                                           |
| Comment / history    | `bd comments add <id> "<text>"`                                           |
| Resolve              | `bd close <id> --reason "<summary + commit sha>" --json`                  |
| Triage role          | a label: `bd label add <id> <label>` (see `triage-labels.md`)             |
| Discovered work      | `bd create ... --deps discovered-from:<id>`                               |

## When a skill says "publish to the issue tracker"

Create the issue with `bd create` (epic for a spec, child issues for tickets) and report the IDs.

## When a skill says "fetch the relevant ticket"

`bd show <id> --json`. The user normally passes the ID directly.

## Closing work

There is no PR flow and no git remote yet: work is closed by merging to the
integration branch locally and running `bd close` with the commit sha.
Until a remote exists, the "push" step of AGENTS.md "Landing the Plane" ends at a local commit.
