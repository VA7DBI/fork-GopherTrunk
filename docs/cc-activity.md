---
layout: page
title: CC Activity panel
description: Live filter view of the trunked control-channel chatter — grants, affiliations, patches, talker aliases
nav_group: Operate
---

# CC Activity panel

GopherTrunk's web console has a **CC Activity** panel (web `/cc`)
that filters the events stream down to the chatter you actually
want to watch live while a trunked system is being decoded:

- **Voice grants** — talkgroup, source radio, frequency, tags (ENC,
  EMERG, DATA)
- **Affiliations** — radio → talkgroup with the response code
- **Registrations** — unit registration / deregistration with the
  response code
- **Patches / dynamic regroups** — the super-group plus member
  count, "add" vs "cancel" verb
- **Talker aliases** — the decoded display-name string per radio
  ID. Two paths feed this: the Motorola vendor TSBK form on the
  control channel, and the Motorola voice-channel form (P25
  Phase 1 LDU1 LCO 0x15 header + N × LCO 0x17 data blocks, run
  through Motorola's reverse-engineered alias cipher). Each
  completed alias is bound to the
  current call's source ID so the Radio IDs panel can persist it
  next to the operator-configured catalogue.
- **CC lock / loss** — control-channel acquisition + recovery
- **Call start / end** — actively-decoded voice transitions

Everything here is already on the events bus today; the panel is a
focused filter view, not a separate subscriber.

## Use cases

- **Onboarding a new system.** Watch the chatter for 30 s and you
  see which talkgroups are active, who's talking to who, whether
  encryption is in use, what the patch / regroup patterns look
  like. Far easier than parsing the raw event log.
- **Coverage debug.** Watch the CC lock / loss markers correlated
  against `grant` flow — if grants pause and a `cc.lost` lands
  followed by `cc.locked` on a different frequency, you have a
  simulcast / coverage handoff and can plan an antenna change.
- **Patch tracking.** Patches and dynamic regroups are easy to
  miss in the raw event firehose; the panel surfaces them with
  super-group and member counts so you can spot when an incident
  patches multiple groups together.

## Filters

- **Kind** — show only one kind of event (Grant / Affiliation /
  Patch / ...). Set to "All kinds" for everything.
- **System** — substring match against the event's `system`
  field. Useful when more than one trunked system is configured.
- **Pause** — freezes the displayed rows; events keep arriving on
  the bus so when you resume you catch up to the latest state.
  Doesn't disconnect SSE.

## What's not here

- **Per-call audio playback.** That lives on the Active panel
  with its own audio player.
- **TSBK / CSBK / OSW raw PDU view.** The CC Activity panel shows
  the *decoded* control-channel chatter (the events the engine
  publishes after the FEC chain has succeeded). A raw-PDU view —
  the bit-level inspector for debugging a misbehaving decoder —
  is a separate follow-up; it would need every per-protocol
  pipeline to publish PDU events on the bus and surface them
  through a developer-mode panel.

## Implementation notes

The panel is pure web — no daemon changes. It reads the rolling
events buffer the shared store maintains (capped at the SSE
consumer's defaults), filters by kind, and renders one row per
matching event with payload-specific formatting:

| Event kind | Row "Details" column |
| --- | --- |
| `grant` / `call.start` | `TG <id> ← <src> @ <freq> MHz · <protocol> · [tags]` |
| `call.end` | `TG <id> · <reason>` |
| `affiliation` / `registration` | `radio <id> → TG <id> · resp <code>` |
| `patch` | `super-group <id> · <N> members · add/cancel` |
| `talker.alias` | `radio <id>: "<alias>"` |
| `cc.locked` / `cc.lost` | `@ <freq> MHz` |

When more event payload shapes land (e.g. encryption metadata
arrives mid-call via `call.encryption`), extending the panel is a
matter of adding a switch arm in `CCActivity.tsx`'s `renderRow`.

Radio IDs in the feed (the `<src>` in a grant row, the `<id>` in an
affiliation / registration / talker-alias row) are rendered as
clickable links into the Radio IDs panel's per-radio detail view —
one click pivots from the live chatter to the radio's recent call
history.
