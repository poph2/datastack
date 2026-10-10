# Logos

Third-party marks used by the stack landscape (`/stack`, `src/lib/stack.ts`).
Every trademark belongs to its owner; they are included here only to identify
the tools DataStack is built from, and the page links each one to its project.

Committing them rather than loading from a CDN is deliberate: the demo has to
work on conference wifi, or none at all.

Provenance:

- `cloudnativepg.svg` — CNCF artwork repository (`cncf/artwork`), colour icon.
- `starrocks.svg`, `tempo.svg` — the CNCF landscape's own logo set
  (`cncf/landscape`, `hosted_logos/`).
- `kind.svg` — `kubernetes-sigs/kind`, `logo/logo.svg`.
- Everything else — [Simple Icons](https://simpleicons.org) (the icon set is
  CC0; the marks themselves are not). These are single-path and carry no fill,
  so the page paints them with each project's brand colour through a CSS mask.

Tools with no mark of their own (GORM, Cobra, pgx, distroless, the CDC daemon,
the model) render as a monogram tile instead; there is no file for them.
