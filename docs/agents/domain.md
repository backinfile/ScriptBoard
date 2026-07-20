# Domain Docs

How engineering skills consume this repo's domain documentation while exploring or changing the codebase.

## Layout

This is a single-context repository:

```text
/
├── CONTEXT.md
├── docs/adr/
└── src/
```

## Before exploring, read these

- Read root `CONTEXT.md` for the project's domain language.
- Read the ADRs in `docs/adr/` that touch the area being changed.
- Treat ADRs marked `superseded` as historical context, not current direction.

If a referenced domain file does not exist, proceed silently. Domain files are created lazily by the domain-modeling workflows when a term or decision is actually resolved.

## Use the glossary's vocabulary

When naming a domain concept in an issue, implementation, test, diagnosis, or proposal, use the canonical term from `CONTEXT.md`. Do not drift to synonyms the glossary explicitly lists under `_Avoid_`.

If a needed concept is absent, reconsider whether it is project-specific language or note the gap for the domain-modeling workflow.

## Flag ADR conflicts

If proposed work contradicts an existing active ADR, surface the conflict explicitly rather than silently overriding it. Cite the ADR and explain why it may need to be reopened.
