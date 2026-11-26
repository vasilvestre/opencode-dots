# 0025. Workspace memory lives in a private repository with one writer

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Memory is split in three stores ([0001](0001-memory-split-in-three.md)) and a
pull request or issue has a worklog ([0006](0006-per-pull-request-memory.md)).
All of it is small markdown under `STATE_DIR` on one machine. With an always-on
VPS that handles repositories while the workstation is off, the same files must
be read and written from several machines.

The files do not all behave the same way:

- `lessons.md` is rewritten whole by the `Lessons` step of `tend-repo`. Two
  machines that rewrite it from different inputs produce two versions that
  cannot be merged line by line.
- `raised.txt`, `memory.md` and the dream inbox are appended to. They merge
  without trouble.
- The worklog of a work item ([0023](0023-tracker-independent-work-item.md)) is
  read first and rewritten at the end of a run.

The data is private (it names repositories and clients), so it must not go to a
third party. Clones, the tower index and secrets are large or sensitive and are
not memory. Without a decision, each machine keeps its own copy and the lessons
learned on the VPS never reach a local session, or the reverse.

## Decision

We organise memory per **workspace**, a set of projects that belong together.
A workspace never mixes clients.

Each workspace has one private git repository on the always-on VPS: a bare
repository reached over the tailnet, with no third party. Identity is the
tailnet. It holds only the small derived markdown files:

- `state/tend/<repo>/lessons.md`, `memory.md` and `raised.txt`
- `dreams/inbox/`
- the markdown linked to a work item, its worklog included
- `inbox/`, described below

It never holds clones, run logs, the tower index or secrets. The tower index is
rebuilt locally from the markdown and is not synced. The history of pull
requests and issues stays in the tracker, where it can be rebuilt. It is not
copied here.

**One writer for `lessons.md`.** The VPS is the only writer. The file is
rewritten whole, so a second writer would conflict. A local session never edits
it.

**Local sessions append.** A local session writes append-only files named
`inbox/<actor>-<host>-<date>.md`. Each entry is tagged with the work item
([0023](0023-tracker-independent-work-item.md)) and carries an actor id from the
first version. Two machines never write the same file, so nothing conflicts.
`tend-repo` folds the inbox into `lessons.md` and `memory.md` on the VPS.

**Checkout and release.** A `checkout` copies one subject to a local machine:
the lessons of its project and the markdown of its work item. It removes no
capability from the cloud, which can still work on the subject. A `release`
pushes the local inbox.

**Sync is automatic.** A user timer pulls and pushes on each local machine. On
the VPS, a trigger after each run commits and publishes the changes. A write to
the repository is done by a shell step, never by the agent
([0002](0002-shell-steps-own-side-effects.md)).

## Consequences

- Merge conflicts are avoided by construction: one writer for rewritten files,
  unique file names for appended ones.
- A local machine lags the cloud by one sync interval. A lesson learned on the
  VPS reaches a laptop at the next pull, and a local inbox entry reaches the
  lessons only after the next `tend-repo` run. Both are acceptable: lessons are
  slow knowledge.
- Local work cannot change a lesson directly. It must go through the inbox and
  wait for a fold. That is slower and is the price of one writer.
- A private repository on the VPS costs disk and a backup, and nothing else.
  No hosting fee.
- A workspace per client keeps data apart and makes deleting a client one
  action.
- The VPS becomes the place where memory is authoritative. If it is down, local
  machines keep working on their last copy and queue their inbox. A read-only
  local sync could later keep that copy current when the VPS is down; this
  record does not define it.
- The inbox grows. Folded files can be archived or deleted by the fold step.

## Alternatives

- **A shared synced folder** (Syncthing, a cloud drive). It conflicts on
  `lessons.md` with no merge history, and a drive adds a third party. Rejected.
- **A database.** [0024](0024-control-plane-database.md) holds the control
  plane, but memory is read by the agent as plain files, and markdown diffs and
  reviews well in git. Rejected for memory.
- **One repository per project.** More repositories to create and sync, and no
  natural place for a lesson shared inside a workspace. Rejected.
- **Letting every machine write `lessons.md`.** Whole-file rewrites from
  different inputs cannot be merged, and a wrong merge corrupts a rule silently.
  Rejected.
