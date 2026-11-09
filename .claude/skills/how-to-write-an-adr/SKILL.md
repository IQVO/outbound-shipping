---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

<!-- TEMPLATE NOTE (warehouse-harness-template v2): adapt every repo-specific example in this file (file paths, type names, field names) to THIS repo real code. Do not copy-paste verbatim. -->

# How to write an ADR

Use when a change is architecturally significant — a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract change,
or anything a future reader would otherwise have to reverse-engineer from
the diff. Not every change needs one: a bug fix or a routine feature
addition inside an already-decided architecture doesn't.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded,
sequential — check the highest existing number
(`git ls-tree --name-only origin/develop -- docs/adr/` and pick the
next integer, never reuse or guess). The existing `0001`–`0005` are
plain markdown; there is no generated docs site in this repo.

## Format: Michael Nygard's template

No frontmatter — this repo's ADRs start directly with a numbered
`# ADR NNNN: Title` heading (ADR 0001 uses the short `# 1.` form), then
Nygard sections:

```markdown
# NNNN. Title (a short noun phrase)

## Status
Accepted | Proposed | Deprecated | Superseded by ADR-XXXX

## Context
The forces at play — technical, business, constraints — that make this
decision necessary. Write in the past tense, as if explaining to someone
who wasn't there. State the alternatives seriously considered, not just
the one chosen; a reader six months from now needs to know a simpler
option was weighed and rejected, not assume nobody thought of it.

## Decision
What was actually decided, stated as an active, present-tense
declaration ("we will...", not "we might..."). Be specific about the
mechanism, not just the intent — this section should let a reader
implement the same decision from scratch without asking follow-up
questions.

## Consequences
What becomes easier, what becomes harder, and what future work this
creates or forecloses. Be honest about the downsides — an ADR that only
lists benefits reads as marketing, not a decision record.
```

The `## Decision` section is the part worth the most editing effort: see
ADR-0005 (`docs/adr/0005-cloudevents-envelope-and-type-catalogue.md`)
for a model example — it pins the exact envelope mechanism, the
type-naming rule and where the catalogue lives, and is specific enough
that `.claude/rules/integration-events.md` and the
how-to-add-an-integration-event skill can point straight at it.

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Add a `## Status` line noting
`Superseded by ADR-XXXX` on the OLD one (a one-line patch), and open the
new ADR referencing it: `**Accepted.** <date>. Supersedes [NN. Old title](./NNNN-old-slug.md).`
(No ADR in this repo has been superseded yet; follow the same wording
when the first one is.)

## Cross-repo decisions: use a companion ADR, not one repo's private opinion

When a decision genuinely spans two bounded-context repos (e.g.
facility-layout's functional-location roles enabling wes-work-planning's
travel-graph feature), write ONE ADR per repo, each referencing the other
explicitly as "the companion ADR" with a one-line description of the
split of responsibility — see facility-layout's ADR-0016/0017 pair. Don't
write the decision once in one repo and expect the other repo's readers
to find it; each bounded context's docs site is read independently.

## After writing: verify what you can

There is no generated docs site to build — `docs/adr/` is plain markdown
committed as-is. Before opening the PR, check that any relative links you
added resolve (`./0005-cloudevents-envelope-and-type-catalogue.md` style)
and that the numbering/heading match the existing files; a broken
relative link is a silent 404 for the next reader.
