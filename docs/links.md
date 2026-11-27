# Link proposals

ADR 0041, first step (the ADR stays `proposed`). A workflow may notice that two work items are one
piece of work, a GitHub issue and a Jira ticket for instance. It writes a **proposal**. A person
accepts or declines it. Nothing is linked, merged or written to a tracker until then, and there is no
automatic mode.

Nothing changes for a deployment that does not use it: migration 6 adds a table and one column, and no
code path creates a proposal unless something calls `propose`.

## The record

`link_proposal` (migration 6): `id` (`lp_` and 128 random bits), `work_item_a`, `work_item_b`,
`confidence` (0 to 1), `reason`, `evidence` (a JSON object), `state` (`open`, `accepted`, `declined`),
`proposed_by`, `created_at`, `decided_by`, `decided_at`. A pair is unique in either order while it is
open (an index on the smaller and the larger id). Once decided, the pair may be proposed again.

The history of a decision is not a second table: every step writes an event on **both** work items in
`work_item_event`, which is append only by use: `link.proposed`, `link.declined`, and on acceptance
`link.accepted` (the kept item) and `link.merged` (the other one). The acceptance events hold the
proposal id and every reference that moved, with its original `source_event`, `attached_by` and
`attached_at`.

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

## Undo

`SplitLink` is not built. The data it needs is kept: the merged item still exists with
`merged_into`, and the `link.accepted` and `link.merged` events list each moved reference with its
original attribution, so a later split can move them back and reopen the item. Until then a merge is
corrected in the database by hand.

## Commands

```bash
bin/repos db link propose -a ID -b ID -confidence 0.8 -reason "same stack trace" [-evidence '{"k":1}'] [-by NAME]
bin/repos db link propose -file proposal.json     # {"a","b","confidence","reason","evidence","proposed_by"}
bin/repos db link list
bin/repos db link accept -id lp_... [-by NAME]
bin/repos db link decline -id lp_... [-by NAME]
```

`-by` defaults to `dot` when proposing and to `cli` when deciding. A workflow writes the proposal as a
file and a shell step inserts it (ADR 0002); the agent never calls the database. A file with an unknown
key, or larger than 64 KiB, is refused.

## Dashboard

`GET /api/links` lists the proposals, open and decided, newest first, with both items (title, repository,
workspace, trust, references). It is read only and answers an empty list when there is no database.

The Links page (`#links`, Ctrl+K, key `0`) shows the open proposals with Accept and Decline, and the
decided ones below. `POST /api/links/accept` and `POST /api/links/decline` take `{"id": "lp_..."}`
with a JSON content type from a trusted host. The id is validated. Both are wrapped by `audited`
(ADR 0037, `docs/audit.md`) with the proposal id as target, and the decider recorded is the caller of
that audit row. A refusal of the store is a 409 with its message. The dashboard holds no GitHub token
and writes nothing outside the database.

## Not done

A workflow that proposes (it needs two wired sources, ADR 0039), `SplitLink`, a link across a trust
boundary that holds references only, `auto` mode, and moving claims or payloads on accept.
