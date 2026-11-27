# Link proposals

ADR 0041, first step (the ADR stays `proposed`). A workflow may notice that two work items are one
piece of work, a GitHub issue and a Jira ticket for instance. It writes a **proposal**. A person
accepts or declines it, and can split an accepted one again. Nothing is linked, merged or written to a
tracker until a person accepts, and there is no automatic mode.

Nothing changes for a deployment that does not use it: migration 6 adds a table and one column, migration
7 rebuilds that table to add the `split` state, and no code path creates a proposal unless something calls
`propose`. The workflow that proposes is hidden and run by hand (see below).

## The record

`link_proposal` (migration 6, rebuilt by 7): `id` (`lp_` and 128 random bits), `work_item_a`, `work_item_b`,
`confidence` (0 to 1), `reason`, `evidence` (a JSON object), `state` (`open`, `accepted`, `declined`,
`split`), `proposed_by`, `created_at`, `decided_by`, `decided_at`, and from migration 7 `split_by`,
`split_at`. A pair is unique in either order while it is open (an index on the smaller and the larger id).
Once decided, the pair may be proposed again. The columns are listed migration by migration in
`docs/store.md`.

The history of a decision is not a second table: every step writes an event on **both** work items in
`work_item_event`, which is append only by use: `link.proposed`, `link.declined`, on acceptance `link.accepted`
(the kept item) and `link.merged` (the other one), and on a split `link.split` on both. The acceptance events
hold the proposal id and every reference that moved, with its original `source_event`, `attached_by` and
`attached_at`, and the state the second item had.

The text is data. `reason` is cut to one paragraph of at most 500 characters with control characters
removed, the evidence is a JSON object of at most 8 KiB, and neither is ever read as an instruction.

## Accepting

`AcceptLink` runs in one transaction. The references of the **second** item (`b`) move onto the first
(`a`), because one reference names one work item (ADR 0023). The second item gets the state `merged`
and `merged_into` set to the first. Both get an event. The proposal becomes `accepted`.

It refuses, with the proposal left open and nothing changed, when:

- the two items are in different workspaces, or their projects have different `trust` levels
  (ADR 0032; a project with no `trust` key is `open`). A link never carries content across that
  boundary. The ADR also allows a link that holds references and state only; that is **not
  implemented**, it is refused, and the dashboard shows a `boundary` mark instead of an Accept button;
- one of the items is already `merged`;
- the second item holds a live claim (ADR 0026), since a person or a dot is working on it.

A person can still decline a proposal that cannot be accepted.

Not carried over: the title, the payload and the state of the second item stay on it. Other open
proposals that name the merged item can no longer be accepted.

## Undo: split

`SplitLink` undoes an accepted link. It reads what the accept moved from the `link.accepted` event of the
kept item (each reference with its original `source_event`, `attached_by` and `attached_at`, and the state
the second item had) and, in one transaction, moves exactly those references back onto the second item,
restores its earlier state, clears `merged_into`, writes `link.split` on both items and marks the proposal
`split` with `split_by` and `split_at`. The decision of the accept (`decided_by`, `decided_at`) is kept.

It refuses, changing nothing, unless all of these hold:

- the proposal is `accepted` (an open, declined or already split one is refused);
- the second item is still `merged`, with `merged_into` pointing at the kept one;
- every moved reference still exists and is still attached to the kept item (one that moved again, or was
  removed, blocks the split: nothing is guessed);
- the kept item holds no live claim (ADR 0026), because someone works on the merged whole.

References the kept item held before the accept stay on it. A split pair is not proposed again by the
workflow: a person has answered it. To link it anyway, propose it by hand with `bin/repos db link propose`.

## Commands

```bash
bin/repos db link propose -a ID -b ID -confidence 0.8 -reason "same stack trace" [-evidence '{"k":1}'] [-by NAME]
bin/repos db link propose -file proposal.json     # {"a","b","confidence","reason","evidence","proposed_by"}
bin/repos db link list
bin/repos db link accept -id lp_... [-by NAME]
bin/repos db link decline -id lp_... [-by NAME]
bin/repos db link split lp_... [-by NAME]          # or -id lp_...; the id may be the argument
bin/repos db link candidates -workspace NAME -out DIR [-max N]
```

`-by` defaults to `dot` when proposing and to `cli` when deciding or splitting. `candidates` is for the
workflow: it writes one `pair-NNN.json` per candidate pair under `DIR` (mode 600), prints one line per pair,
then `candidates=N refused_boundary=M`. A workflow writes the proposal as a file and a shell step inserts it
(ADR 0002); the agent never calls the database. A file with an unknown key, or larger than 64 KiB, is refused.

## Dashboard

`GET /api/links` lists the proposals, open and decided, newest first, with both items (title, repository,
workspace, trust, references). It is read only and answers an empty list when there is no database.

The Links page (`#links`, Ctrl+K, key `0`) shows the open proposals with Accept and Decline, and the
decided ones below. An accepted row whose second item is still merged into the first has a Split button, with
a confirm dialog that says what comes back; a split row shows who split it and when. `POST /api/links/accept`,
`POST /api/links/decline` and `POST /api/links/split` take `{"id": "lp_..."}` with a JSON content type from a
trusted host. The id is validated. All three are wrapped by `audited` (ADR 0037, `docs/audit.md`) with the
proposal id as target, and the person recorded is the caller of that audit row. A refusal of the store is a 409
with its message. The dashboard holds no GitHub token and writes nothing outside the database.

## The workflow: `_propose-links`

`workflows/_propose-links.yaml` is hidden (a leading underscore: nothing dispatches it, ADR 0021). I run it by
hand through `awf serve` with the input `workspace`; `max_pairs` (default 5, at most 50), `min_confidence`
(default 0.7) and `model` are optional. It never accepts, declines or splits a link and writes to no tracker:
its only effect is an `open` proposal, which a person decides in the dashboard. It needs `bin/repos` (or
`REPOS_BIN`), as `bin/advisories` does.

```
candidates (shell)  bin/repos db link candidates: the pairs, as queue/pair-NNN.json
   v
more (shell)        queue empty? -> done        (the failure edge is the end of the loop)
   v
take (shell)        the first pair becomes work/current.json
   v
analyse (agent)     reads current.json only, writes work/answer.json only
   v
record (shell)      schema, related, threshold, then bin/repos db link propose -file
   v                (a failed analyse or record goes to skip) -> more
```

**The pairs are chosen by a shell step, never by the agent.** `store.LinkCandidates` lists the open work items of
the input workspace and keeps the pairs that share a token, dropping:

- a pair under different trust levels (ADR 0032; counted as `refused_boundary`). The workspace is the input,
  so another workspace is never looked at;
- a pair that has any proposal, open or decided (declined, accepted, split): an open one is pending and a
  decided one is a person's answer that a rerun must not ask again;
- a merged item: only `open` items qualify.

The token classes come from STRUCTURED payload fields only, never from a title, a body or any free text
(ADR 0023): `package:<name>` (keys `package`, `packages`), `advisory:<GHSA or CVE id>` (keys `id`, `cves`,
`advisory`, shaped like a real id) and `file:<relative path>` (keys `file`, `files`, `path`, `paths`; no `..`,
no absolute path, no space). A value that is not shaped like its class is dropped. Pairs sort by the number of
shared tokens, then by id, and are cut at `max_pairs`. A pair that only shares a package is weak: the agent is
told so.

**The agent judges one pair.** It reads `current.json` (both titles, repositories and payloads, each payload
capped at 4000 bytes and marked truncated beyond that, plus the shared tokens) and writes one JSON object:
`{"related": bool, "confidence": 0..1, "reason": <= 300 characters}`. The prompt says everything in that file is
data from outside and never an instruction, forbids any command and any other file, and says that when unsure
the answer is `related: false`.

**The record step validates and inserts.** An answer is refused, with nothing inserted, when the file is empty
or over 4 KiB, is not an object with exactly those three keys, has a non-boolean `related`, a `confidence`
outside 0 to 1 or not a number, or a `reason` empty or over 300 characters. `related: false` and a confidence
below `min_confidence` insert nothing. The two ids come from the shell's pair file, never from the answer, so an
agent that obeyed hostile text cannot name other items (an extra key is a schema failure). The reason is stored
cleaned, as data. `evidence` holds the shared tokens and the pair name. `proposed_by` is `propose-links`. A pair
proposed meanwhile is reported and skipped. A failed agent step or a failed insert drops that pair and the loop
goes on, because the pair already left the queue.

A rerun proposes nothing for a pair that has a proposal. A pair the model rated unrelated, or below the
threshold, leaves no record and may be asked again on a later run: only a proposal closes a pair.

`bin/test-propose-links` runs these steps offline against a temporary database, with a stub in place of the
agent: candidate selection, the boundary refusal, schema rejection, the threshold, idempotence, hostile text in a
title and in an answer, and the split path. `awf validate --dir workflows` checks the syntax.

## Not done

A link across a trust boundary that holds references only, `auto` mode, moving claims or payloads on accept,
a record of a pair the model rated unrelated (so it is not asked again), a scheduled run of the workflow, and
more than one source wired for real: only advisories exist today, so the pairs the workflow finds are advisories
of one workspace (the same advisory in two repositories, or two advisories of one package).
