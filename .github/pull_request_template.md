<!--
Thanks for the PR. Please fill in the sections below. If a section
doesn't apply, write "n/a" rather than deleting it — that makes review
go faster.

PR scoping rules live in CONTRIBUTING.md. Briefly: one logical change
per PR; bug fixes ship with a regression test; refactors stay separate
from behaviour changes.
-->

## Summary

<!-- One or two sentences. Why is this change needed? What's broken or
     missing today? -->

## Changes

<!-- Bulleted list of what changed. The diff itself documents the
     "how"; this list helps reviewers navigate. -->

-

## Test plan

<!-- How did you verify this? Which tests cover the new behaviour? Any
     manual smoke tests against hardware? -->

- [ ] `make vet test` is green locally
- [ ] `make integration` is green locally (if the change touches the
      daemon)
- [ ] Added or updated tests where appropriate
- [ ] Smoke-tested against real hardware (if SDR / USB / vocoder code
      changed) — describe the dongle / capture used

## Breaking changes

<!-- Does this change config-file keys, CLI flags, REST endpoints,
     gRPC schemas, or default behaviour an existing operator depends
     on? If yes, describe the migration path. If no, write "none". -->

## Docs / CHANGELOG

- [ ] Added a `## [Unreleased]` bullet to [`CHANGELOG.md`](../CHANGELOG.md)
      if this is user-visible
- [ ] Updated [`README.md`](../README.md) or [`docs/`](../docs/) if
      operator-facing behaviour changed

## Linked issues

<!-- Closes #NNN / Refs #NNN. Leave blank if there's no tracked issue. -->
