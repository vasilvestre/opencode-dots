# Use cases

Who runs a dot, where, with whom, and what each of them expects. This is a map of
the situations we want to serve or deliberately leave out. It is not a promise:
the last section says what exists today and which decision record covers each gap.

Each case has a stable id (A1, B2, ...) that issues and records cite.

## Axes

| Axis | Values |
| --- | --- |
| Topology | local, remote server, local plus server (hybrid executor), several independent servers |
| Humans | one, a team, several teams, outsiders (clients, contributors) |
| Owner of the dot | a person, a company, a client, a community |
| GitHub identity | personal account, dedicated bot account, GitHub App, company account |
| Trust of the code | public, private personal, a third party's, code that must not leave |
| Source of work | GitHub issues and pull requests, vulnerable dependencies, Sentry issues, a company tracker |
| Autonomy ceiling | `observe`, `propose`, `act`, `auto` (the code of a whole repository), see ADR 0040 |
| Class of an item | complete fix, fix with reinforced review, human diagnosis (ADR 0040) |
| Who pays for models | me, the team, the client, a quota per person |
| Availability | machine on and off, always on |

## A. One person

- **A1. Solo, local dot.** The dot runs on my workstation and stops when it does.
  Expects: no infrastructure, a dashboard on loopback, a review of my open pull
  requests, a morning summary. Nothing leaves the machine except what the model
  provider sees. Limit: nothing happens while the machine is off.
- **A2. Solo, dot on a remote server.** Always on, reached over the tailnet.
  Expects: nothing lost, an alert when something blocks, a dashboard readable on
  a phone, backups with a tested restore, and a plain answer to "is my dot alive?".
- **A3. Solo, hybrid.** The server watches GitHub, my workstation executes with my
  models, keys and clones. Expects: a task waits for the workstation without being
  lost or run twice, and the UI says "waiting for your machine".
- **A4. Solo, many repositories, some disposable.** Expects: a budget per
  repository, profiles ("managed", "observe only"), a one-click pause per
  repository, and a ranking of where the money went this week.
- **A5. Solo, open source maintainer.** The dot reads issues and pull requests from
  strangers, the highest prompt-injection exposure. Expects: outside text always
  treated as data, cautious comments, filters by author, label and first
  contribution, and a log of what it read.
- **A6. Solo, recognisable actions.** Expects to tell at a glance what is mine and
  what is the dot's.

## B. A team, one dot

- **B1. Shared dot on a server, under one person's identity.** Today's state and the
  most fragile one. Teammates expect to know it is the dot speaking, to say "do not
  touch my branch" (claims), "take this one" and "stop".
- **B2. Shared dot with its own identity** (bot account or App). Any author can
  delegate their own pull request. Expects: minimal rights per repository,
  identifiable commits, a trace of who delegated what.
- **B3. Mandatory automatic review.** The dot is a check like CI. Expects: a clear
  verdict, a consistent tone, a review charter versioned in the repository, and a
  way to contest a comment that feeds back into the lessons.
- **B4. Roles.** Developer (fast review, answers, "take it"), tech lead (overview,
  rules, costs, history), manager (what shipped this week, no code), admin (keys,
  quotas, audit), observer (read only).
- **B5. Several repositories and sub-teams.** Separate workspaces, lessons per team,
  settings per repository. A lesson learned in one repository does not leak into
  another of a different confidentiality.
- **B6. The team pays for models.** Monthly budget, cost per pull request, overrun
  alerts, a cap per person, and which tasks are worth their cost.

## C. Several dots that meet

- **C1. My personal dot and my company's dot.** Two independent instances of the same
  engine under two profiles (ADR 0035): the personal one at `auto` on repositories I
  maintain, the company one on a private network at `act` at most. Two contexts I
  pick per device. The personal dot never sees a client repository. Expects: a
  context selector with a strong visual cue, a unified view of metadata only
  ("3 reviews wait for me"), and notifications that carry no content.
- **C2. Two dots on the same repository.** Risk: double review, contradictory
  comments, two pushes. Expects: a lock per repository or pull request (claims
  extended to bots) and a priority rule: the team's dot comments, mine only keeps
  private memory. A dot recognises another dot and defers to it.
- **C3. One dot per teammate, each on their own machine.** No collisions (claims),
  lessons shareable on choice, a shared charter, a team dashboard that aggregates
  without exposing each person's private settings.
- **C4. One dot asks another.** An explicit protocol, never free text read as an
  instruction, full traceability.
- **C5. Moving a repository from one dot to another.** Memory, lessons and history
  move without copying by hand, and nothing stays behind on the old server.

## D. Company, clients, compliance

- **D1. Consultant with several clients.** Each has its own rules: no cloud, a
  mandated model provider. Allow-lists of models, providers and executors per
  client, a refusal of any contradicting configuration, memory strictly separated,
  never a client's clone on another's machine, and an export of what was sent to
  the model.
- **D2. Regulated company** (GDPR, ISO, SOC 2). An immutable audit log, configurable
  retention, a right to erasure (purge a repository, a person, a client), EU
  hosting, a register of sub-processors, proof that code is not used for training.
- **D3. "Strict" repository.** Self-hosted model only, no checkout outside the named
  server, no code in logs. An honest degraded mode: "I cannot do this here".
- **D4. A client who pays and wants to see.** Read access to what the dot does on
  their repository, its costs, a guarantee that no other client appears, a
  periodic report.
- **D5. Closed network** (bastion, VPN, MFA). No public entry, secrets in a vault,
  token rotation, SSO for the dashboard.
- **D6. Platform team offering the dot as a service.** Self-service onboarding (a
  pull request on the repository list), quotas, internal billing, an SLA.

## E. Edge and degraded cases

| Case | Expectation |
| --- | --- |
| Workstation off, task waiting | a visible queue, resumption, no duplicate |
| Server down | the workstation takes over or warns, nothing lost |
| Two dots push the same branch | claim, refusal, explicit message |
| A human pushes while the dot works | the dot stops and never forces |
| Model provider down or limited | fall back to another model or defer, never a half-written comment |
| Token expired or revoked | immediate alert, no silent failure |
| Huge pull request or monorepo | splitting, a cost cap per task, a clean abort |
| Malicious pull request (prompt injection) | text stays data, no action outside scope |
| Repository archived, renamed, transferred | memory follows or is marked orphaned |
| A colleague leaves | their claims expire, their delegations lapse |
| The dot is wrong | easy undo, the correction feeds the lessons |
| Cost runaway | a hard cap, not only an alert |

## F. Sources of work

- **F1. Vulnerable dependencies.** `composer audit`, advisories and Dependabot alerts
  become work items. Expects: a check that the package is installed and the
  vulnerable code reachable, an upgrade pull request or a justified ignore, and
  nothing merged at a low ceiling.
- **F2. Production errors.** A Sentry issue becomes a work item with its stack trace
  as data, never as instruction. Expects: a class (complete fix, reinforced review,
  human diagnosis), a pull request when the cause is local, and a note on the Sentry
  issue otherwise.
- **F3. Company tickets.** A ticket in a company tracker maps to a repository. An
  unmapped project stays diagnosis-only. The note on the ticket is internal.
- **F4. One cause, three reports.** An issue, a ticket and a Sentry issue describe the
  same defect. Expects: the dot proposes the link, a person or a threshold confirms
  it, and one pull request closes the cluster (ADR 0041). Across a trust boundary
  the link carries references, not content.

## G. Autonomy

- **G1. A repository the dot owns.** At `auto` it owns the code: picks the code work
  worth doing, keeps dependencies current, reviews and merges. It never deploys,
  never publishes a release, never sets priorities: those are CI/CD and project
  management.
- **G2. A repository the dot only watches.** `observe`: analysis and internal notes,
  no patch.
- **G3. Everything waits for a click.** `propose`: the patch is prepared and held in
  the dashboard until a person publishes it.
- **G4. A local worker.** A developer's workstation pulls the tasks it may take from
  the company instance and runs them with its own models (ADR 0042). A strict
  repository is never sent to it.

## Expectations by theme

**Visibility.** An activity feed per repository, pull request and person. Live state
(running, waiting and for what, failed). An explanation of each decision, including
why it did not push. Costs and durations at every level. One page for the health of
the dot (server, executors, models, tokens). A reduced view per role.

**Collaboration.** Talk to the dot inside GitHub, not only on the dashboard. Visible
claims. Lessons shared after human review. Contest or correct a review, with a
trace. Hand over from human to dot, dot to human, dot to dot. A tone and charter per
team.

**UI and UX.** Always know the context (personal, company, client). A first launch
without YAML or ADRs. A phone view for an alert and a "stop this". Dosed
notifications without sensitive content. An emergency stop per repository and
global. Errors that say what to do. Config edits with a diff and a way back.

**Features.** Existing: review, reply, issue to pull request, red-check fix, daily
lessons. To imagine: pull request summary for managers, vulnerable dependency
detection, issue triage, changelog, documentation review, dormant pull request
detection, assisted migration, weekly report per team. Reusable rule profiles, a
dry-run mode, and a sandbox to try a rule on old pull requests.

**Confidentiality.** The server is the strong boundary, one per trust setting
([0029](../ADR/0029-independent-servers-and-contexts.md)). The trust level is the
fine one ([0032](../ADR/0032-trust-levels-per-repository.md)). Secrets never enter
git, logs or prompts. Know exactly what leaves for which provider. Memory per
workspace, never mixed across clients. Retention and a real purge. The dashboard
never on a public interface. The agent writes nothing remote, shell steps do
([0002](../ADR/0002-shell-steps-own-side-effects.md)).

## Where the project stands

Built means code and tests exist. Wired means a workflow or service calls it. State on 2026-11-27: the core has
run end to end on scratch repositories, on `opencode-dots` itself (at `auto`), and on a company repository
through a server and a worker.

| Cases | State | Missing | Record |
| --- | --- | --- | --- |
| A1 to A3 | work today: health page and alert, one tick timer per machine, backups of the workstation and the server | | 0027, 0031, 0049 |
| A4 | `autonomy` (observe is the dry run), the circuit breaker, a daily USD cap per repository, an emergency stop per repository or global (docs/pause.md) | | 0036, 0040 |
| A5 | outside text is data | first-contribution filters, read log | |
| A6, B1, B2 | an opt-in identity marker per repository or profile | a dedicated account or bot mode | 0034 |
| B3 | review charter read from the default branch; reviews run end to end | contesting a comment | |
| B4, B6, D2, D4, D6 | sign-in with GitHub, visibility by repository, audit of writes | roles beyond owner and visitor, client access, quotas | 0037, 0046, 0047 |
| Personal and company profiles | one engine, a policy profile per repository | | 0035 |
| C1 | independent servers, a context per device | unified view | 0029 |
| C2 to C5 | dots do not talk: a person avoids a repository on two dots | migration tool | 0045 |
| D1, D3 | trust levels; every push of a repository with a `sandbox` block goes through the patch guard and, if set, a container test (required on client and strict) | evidence export | 0032 |
| F1 | advisory adapter | polling, reachability check, write-back | 0039 |
| F2, F3 | Sentry and tracker adapters | polling or webhook endpoint, signature check, write-back | 0039 |
| F4 | link proposals with a person's confirmation | automatic linking | 0041 |
| G1 to G3 | publish gate in the workflows, merge scope, classification in `fix-issue`, held items, upstream issue filing, a daily cap | `propose` on a fork needs `push-repo` by hand | 0040, 0043 |
| G4 | workers pull jobs from a queue, with a revocable token, over one public entry | revision check for a stale clone | 0042, 0048 |

## Open: what is offered

Whether the engine is ever sold, and where a public part ends and an offered part begins, is
not decided. The project is open source today, and ADR 0035 keeps the sources, the runner and
the workflows separable so that the question can wait.
