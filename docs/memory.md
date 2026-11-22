# Why the memory is split in three

The dot keeps knowledge in three places: one file per repository for its rules
(`state/tend/<repo>/lessons.md`), one for its facts (`memory.md`), and one queue
of rules that hold beyond the repository (`~/dreams/dreaming/inbox/`), which the
nightly dream pass folds into `~/dreams/MEMORY.md`. This page records what the
research measures, and which part of the design each measurement supports.

Nothing here is a controlled test of this stack. No paper compares "one global
memory file injected everywhere" against "per-repository lessons", so the design
is an inference from adjacent measurements. Where a number is missing, this page
says so.

## 1. A small curated memory beats a large raw context

| Measurement | Source |
| --- | --- |
| Answering from the evidence alone scores 0.870 on LongMemEval; adding the ~115k-token history drops it to 0.606, a 30.3% fall. Llama 3.1 70B falls from 0.744 to 0.334 (−55.1%). | LongMemEval, arXiv 2410.10813, Figure 3(b) |
| Retrieving the 5 relevant observations scores 41.4 overall QA F1, against 37.8 for a 16k-token full-context model, and 44.7 vs 2.1 on adversarial questions. | LoCoMo, arXiv 2402.17753, Tables 2 and 3 |
| Focused prompts of about 300 tokens beat full prompts of about 113k tokens on LongMemEval across every model tested. The report plots the per-model gaps, it publishes no numeric table. | Context Rot, Chroma technical report, July 2025 |
| Paging into the full history instead of a lossy summary lifts deep-memory retrieval from 32.1% (GPT-4) to 92.5% accuracy. | MemGPT, arXiv 2310.08560, §3.1.1 |
| A consolidated memory of 1,764 tokens scores 66.88 on LoCoMo against 72.90 for 26,031 tokens of full context, at 1.44 s p95 latency against 17.12 s. | Mem0, arXiv 2504.19413, Table 2 |

The last row is the honest counterweight: the big context won on quality. What
the small memory buys is cost and latency, roughly 15x fewer tokens and 92%
lower p95 latency, plus robustness when the history contains distractors. Our
workflows have no latency budget to spend, but every agent pass pays for every
token it reads, and the repo checkout is now in context too.

**What we do:** the injected files are capped at 15 and 25 lines. That cap is
extrapolation from this trend, not a measured optimum.

## 2. Consolidation carries the gain, not accumulation

| Measurement | Source |
| --- | --- |
| Removing the reflection step from a generative agent drops believability from 29.89 to 26.88 TrueSkill (d = 8.16 for the full ablation). | Generative Agents, arXiv 2304.03442, §6.5.1 |
| Removing link generation and memory evolution drops multi-hop F1 from 27.02 to 9.65 on LoCoMo. | A-MEM, arXiv 2502.12110, Table 3 |
| A-MEM answers with 2,520 tokens per question against 16,910 for LoCoMo-baseline and 16,977 for MemGPT. | A-MEM, Table 1 and §4.3 |
| Episodic memory is capped on purpose: "we bound mem by a maximum number of stored experiences, Ω (usually set to 1-3)". | Reflexion, arXiv 2303.11366, §2 |

Rewriting a file every pass, dropping the lines the new material contradicts, is
the behaviour those ablations reward. Appending every observation would be the
opposite.

**What we do:** `learn` rewrites `lessons.md` and `memory.md` instead of adding
to them, and the prompts say to drop contradicted lines.

## 3. Sharing pays only when the knowledge is abstract

| Measurement | Source |
| --- | --- |
| Induced workflows reused across tasks raise step success from 18.6 to 35.5 in the cross-domain split of Mind2Web. | Agent Workflow Memory, arXiv 2409.07429, Table 4 |
| The same paper scopes its memory per website, "we group examples by their associated websites". | AWM, §3 |
| One shared pool across six coding benchmarks gives +3.7% pass@3 over zero-shot with high-level insights, and the abstract states that "low-level traces often induce negative transfer due to excessive specificity". | Memory Transfer Learning, arXiv 2604.14004, Table 1 and abstract |
| The same pool loses ground on two benchmarks: TerminalBench2 0.270 for trace memory against 0.315 zero-shot, MLGym-Bench 0.583 against 0.667. | MTL, Table 1 |
| Forward transfer on ALFWorld A to B: raw experience −9.5 points, insights +6.5. On the hard subset raw experience costs −26.1: "the easy subset is largely preserved while the hard subset degrades sharply". | When Continual Learning Moves to Memory, arXiv 2604.27003, Table 3 |
| MindAct 18.6 vs AWM 35.5 is the same table as above, seen from the other side: the gain comes from abstraction, not from volume. | AWM, Table 4 |

This is the part that decides the design. Reuse works, but only for the
abstracted form: a rule phrased without the repository it came from. Concrete
traces and site-specific procedures hurt when moved.

**What we do:** `promote.md` accepts only lines that read in any project and
name no repository, at most three per pass, and the line has to be new: the
prompt forbids repeating what `lessons.md` and `memory.md` already say.

## 4. Memory causes silent regressions

| Measurement | Source |
| --- | --- |
| On tasks the agent solves without memory and fails with it, "52% of the failures trace to a memory item the agent followed, most often a wrong one" (wrong item 20%). | Don't Just Trust Your Memory, arXiv 2611.14320, Figure 4 |
| Lessons-only memory regresses 6.5% of otherwise-solved tasks; attaching the evidence to each item lowers that to 4.5%. | same paper, abstract |
| A memory built from failed runs alone raises regression to 9.3% on PlanCraft and 9.4% on ScienceWorld. | same paper, §5 |

A wrong line in a global file damages every repository at once. That is why the
promotion path runs through the dream pass, which deduplicates against
`MEMORY.md` and rewrites rather than appends, and why a candidate sits in a
queue before it becomes memory.

**What we do:** the dot never edits `~/dreams/MEMORY.md`. It writes an inbox
entry with its evidence, `tend pass on <repo>, <date>`, which is the linked
form this paper measures as better.

## 5. A shared store is an attack surface

| Measurement | Source |
| --- | --- |
| Injected records reach the memory with 98.2% average success and drive the agent to the attacker's outcome with 76.8% average attack success, assuming "a shared memory bank is adopted to support the execution of all user queries". | MINJA, arXiv 2503.03704, abstract and Table 1 |
| The utility cost of the attack stays under 2% on three of four datasets. | MINJA, Table 1 |
| More benign traffic dilutes a poisoned item: attack success 68.9 with 25 benign queries, 31.1 with 100. | MINJA, Table 4 |

Every line the dot writes into a shared file comes from an agent reading
untrusted text: pull request bodies, review comments, issue threads. The
workflows already state that such text is data, not instruction, and that rule
now has a measured reason behind it.

**What we do:** promotion is derived, not quoted. The line must be a rule the
pass wrote itself, it passes through the dream pass, and `~/dreams` is not
mounted in the container.

## 6. Two stores is the shape biology suggests

Complementary Learning Systems theory argues for a fast store that learns single
items and a slow store that interleaves them: "these models discover the
structure in ensembles of items if learning of each item is gradual and
interleaved with learning about other items", while the hippocampal system
"permits rapid learning of new items without disrupting this structure"
(McClelland, McNaughton, O'Reilly, 1995, *Psychological Review* 102(3), PMID
7624455). Catastrophic interference is the failure it avoids: "new learning may
interfere catastrophically with old learning when networks are trained
sequentially" (McCloskey and Cohen, 1989, *Psychology of Learning and
Motivation* 24).

Both are models of animal and network learning, not measurements of LLM agents.
They support the shape of the design, one fast per-repository store and one slow
global one, and nothing about its parameters.

## What is not established

- No paper compares global against per-repository scope for agent memory. The
  closest is AWM, which chooses per-website grouping without ablating the
  alternative.
- The 15 and 25 line caps are extrapolation from the token-count results above.
- Three of these sources are 2026 preprints (2604.14004, 2604.27003,
  2611.14320), not peer-reviewed.
- Every benchmark here uses its own judge and its own baseline, so the
  percentages are not comparable across rows.
- Nothing measures this stack. The only evidence that it works is that the
  workflows run and the files stay short.

## How to check it on this stack

Two numbers, both cheap, over a few weeks:

```bash
# 1. Is a rule written twice, in two repositories?
sed -n 's/^- //p' state/tend/*/*/lessons.md | sort | uniq -d

# 2. Is the global memory growing without consolidation?
git -C ~/dreams log --oneline -20 -- MEMORY.md
```

A repeated rule across two repositories means the promotion step is missing
something. A `MEMORY.md` that keeps growing without the dream pass dropping a
line means the slow store is not consolidating. Neither script exists yet; the
commands are what to run by hand.

Next:

```bash
sed -n '/How a lesson leaves the repository/,/^## /p' docs/workflows.md
```
