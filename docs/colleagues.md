# The dot, for a colleague

A draft. It says what exists today, for one person's repositories. What a colleague gets on a shared
or client project (access, who pays for the models, who may claim a branch) depends on decisions not
taken yet, listed at the end.

## What it is

An agent that watches repositories and handles the routine work on them: it reviews pull requests,
answers review comments, turns an issue into a pull request, fixes a red check, and once a day reads
what happened in a repository to keep a short list of lessons. It runs on a machine of its owner and
talks to GitHub with the owner's identity. The page `docs/workflows.md` describes each workflow.

## What it may and may not do

- **The agent writes no remote.** It reads, thinks and writes files. A shell step does every push,
  comment and pull request, so what leaves the machine is a few lines of shell you can read
  (ADR 0002).
- **Text is data.** A pull request body, a comment, an issue or a diff is never an instruction to
  the agent. The one exception is explicit: the owner's own `/dots take` and `/dots stop`, below.
- **It never merges unless told to.** `auto_merge` is off by default; where it is on, the dot asks
  GitHub to merge after a review that says merge, never before. On a shared server it is off.
- **It opens at most one pull request a day per repository** from its daily pass, and files an issue
  only for a defect it can show.

## Talking to it

| You write | What happens |
| --- | --- |
| a review comment on a pull request the dot watches | it answers, and changes the code when the comment asks for it |
| `/dots take` alone on a line, in a comment or the body | the owner lets the dot fix and push that pull request. Only the owner's login counts, from anyone else the line is ignored |
| `/dots stop` alone on a line | the owner takes the delegation back |
| a question on the dashboard's Ask page | it answers about that pull request, and the exchange is kept |

The dot never reads another line as a command.

## Working on the same branch

The dot pushes to a branch it was delegated. If you work on that branch too, say so: a claim
(ADR 0026, `docs/claims.md`) tells the dot to wait, and it never pushes to a branch a human holds.
Nothing runs claims for you yet.

## What it costs

Each run records its cost, read from awf and shown on the dashboard. The models today are a free
or cheap default for most steps, and a stronger one for the review. Who pays when several people
use one dot is not decided.

## Not decided yet

- **Access.** How a colleague reaches a dot that is not theirs: a shared tailnet node, the company's
  own network or an SSH bastion (ADR 0029). Do not put a colleague on the owner's personal tailnet.
- **Which repositories.** Personal and client work sit on different servers (ADR 0029). Nothing of a
  client goes through the personal one.
- **Which models see the code.** A repository can carry a trust level (ADR 0032), proposed, not
  built. Until it exists, ask the owner which models a repository uses.
- **Whose credentials.** A personal Claude subscription is not shared. Each person brings their own,
  or the team gets API keys.

## Where to read more

`README.md` (setting it up), `docs/workflows.md` (each workflow), `ADR/` (why it is built this way,
with the status of each decision).
