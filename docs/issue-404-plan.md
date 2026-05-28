# Issue 404 Plan

This branch is focused on external call-export sinks for completed calls.

Current state:
- The recorder already publishes `KindCallComplete` after the WAV is flushed.
- The broadcast manager already fans completed calls out to MP3-backed uploaders such as Broadcastify Calls, RdioScanner, OpenMHz, and Icecast.

Planned work:
1. Add a first-class JSON webhook sink so operators can forward completed calls to custom systems and analytics pipelines.
2. Add a local file spool sink so completed calls can be queued on disk for later ingestion.
3. Wire the sinks into YAML config and startup so they behave like the existing broadcast backends.
4. Cover the sinks with HTTP and filesystem tests that verify metadata and MP3 payload emission.
5. Keep the interface generic so later work can add more export targets without changing the recorder path again.

Non-goals for this branch:
- Reworking the recorder event model.
- Changing existing Broadcastify/OpenMHz behaviour.
- Building more exotic export targets yet.