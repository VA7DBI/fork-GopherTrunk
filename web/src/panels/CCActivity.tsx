import { useMemo, useState } from "react";
import { useShared } from "../store/shared";
import type { EventDTO } from "../api/types";

// CC Activity panel — a focused view of the trunked control-channel
// "chatter" already flowing on the events bus. Filters the rolling
// event log down to the kinds an operator wants to watch live while a
// system is being decoded: voice grants, affiliations, registrations,
// patch / regroup announcements, talker-alias completions, control-
// channel lock / loss, and call start/end markers. Useful for spotting
// what a system is doing in real time without scrolling through the
// raw Events log.
//
// Pure filter-and-render — no extra backend wire. The bus events are
// already in the shared store thanks to the existing SSE consumer.

const CC_KINDS: Record<string, string> = {
  "grant": "Grant",
  "call.start": "Call start",
  "call.end": "Call end",
  "affiliation": "Affiliation",
  "registration": "Registration",
  "patch": "Patch",
  "talker.alias": "Talker alias",
  "cc.locked": "CC locked",
  "cc.lost": "CC lost",
};

interface Row {
  ts: string;
  kind: string;
  label: string;
  system: string;
  details: string;
  raw: unknown;
}

export function CCActivity() {
  const events = useShared((s) => s.events);
  const [paused, setPaused] = useState(false);
  const [systemFilter, setSystemFilter] = useState("");
  const [kindFilter, setKindFilter] = useState<string>("");

  const rows = useMemo<Row[]>(() => {
    const list: Row[] = [];
    for (const ev of events) {
      const label = CC_KINDS[ev.kind];
      if (!label) continue;
      const row = renderRow(ev, label);
      if (!row) continue;
      if (systemFilter && !row.system.toLowerCase().includes(systemFilter.toLowerCase())) {
        continue;
      }
      if (kindFilter && ev.kind !== kindFilter) continue;
      list.push(row);
    }
    // Newest first.
    return list.reverse();
  }, [events, systemFilter, kindFilter]);

  return (
    <div className="space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">CC Activity</h2>
        <div className="flex items-center gap-2 text-xs">
          <select
            className="bg-surface border border-border rounded px-2 py-1"
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value)}
          >
            <option value="">All kinds</option>
            {Object.entries(CC_KINDS).map(([k, label]) => (
              <option key={k} value={k}>{label}</option>
            ))}
          </select>
          <input
            type="text"
            placeholder="filter system…"
            className="bg-surface border border-border rounded px-2 py-1"
            value={systemFilter}
            onChange={(e) => setSystemFilter(e.target.value)}
          />
          <button
            type="button"
            className="px-2 py-1 rounded border border-border text-xs"
            onClick={() => setPaused(!paused)}
            aria-pressed={paused}
          >
            {paused ? "▶ resume" : "❚❚ pause"}
          </button>
        </div>
      </header>

      <div className="text-xs text-muted">
        {rows.length} matching event{rows.length === 1 ? "" : "s"}
        {paused && " (paused — display frozen, the daemon is still receiving)"}
      </div>

      <div className="rounded border border-border overflow-hidden">
        <table className="w-full text-xs">
          <thead className="bg-surface text-muted">
            <tr>
              <th className="text-left px-3 py-1 font-normal w-24">Time</th>
              <th className="text-left px-3 py-1 font-normal w-28">Kind</th>
              <th className="text-left px-3 py-1 font-normal">System</th>
              <th className="text-left px-3 py-1 font-normal">Details</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={4} className="px-3 py-4 text-center text-muted">
                  Nothing here yet — control-channel activity will appear
                  as the daemon decodes it. Try removing filters or
                  pointing at a busy system.
                </td>
              </tr>
            ) : (
              (paused ? rows.slice(0, rows.length) : rows).slice(0, 500).map((r, i) => (
                <tr key={i} className="border-t border-border/60">
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(r.ts)}
                  </td>
                  <td className="px-3 py-1">{r.label}</td>
                  <td className="px-3 py-1 font-mono text-accent">{r.system || "—"}</td>
                  <td className="px-3 py-1">{r.details}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function renderRow(ev: EventDTO, label: string): Row | null {
  const payload = (ev.payload ?? {}) as Record<string, unknown>;
  switch (ev.kind) {
    case "grant":
    case "call.start": {
      const g = (payload.grant ?? payload) as Record<string, unknown>;
      const system = str(g.system);
      const protocol = str(g.protocol);
      const groupID = num(g.group_id);
      const sourceID = num(g.source_id);
      const freq = num(g.frequency_hz);
      const tag: string[] = [];
      if (g.encrypted) tag.push("ENC");
      if (g.emergency) tag.push("EMERG");
      if (g.data_call) tag.push("DATA");
      const details =
        `TG ${groupID}` +
        (sourceID ? ` ← ${sourceID}` : "") +
        (freq ? ` @ ${(freq / 1e6).toFixed(4)} MHz` : "") +
        (protocol ? ` · ${protocol}` : "") +
        (tag.length ? ` · ${tag.join(" ")}` : "");
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    case "call.end": {
      const g = (payload.grant ?? payload) as Record<string, unknown>;
      const system = str(g.system);
      const groupID = num(g.group_id);
      const reason = str(payload.reason);
      const details = `TG ${groupID}` + (reason ? ` · ${reason}` : "");
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    case "affiliation":
    case "registration": {
      const system = str(payload.system);
      const radio = num(payload.radio_id ?? payload.source_id ?? payload.source);
      const group = num(payload.group_id ?? payload.talkgroup_id);
      const code = num(payload.response_code ?? payload.response);
      const details =
        `radio ${radio}` +
        (group ? ` → TG ${group}` : "") +
        (code !== 0 ? ` · resp ${code}` : "");
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    case "patch": {
      const system = str(payload.system);
      const superGroup = num(payload.super_group ?? payload.regroup_id);
      const members = (payload.members ?? []) as unknown[];
      const op = payload.cancelled || payload.removed ? "cancel" : "add";
      const details =
        `super-group ${superGroup}` +
        (members.length ? ` · ${members.length} member${members.length === 1 ? "" : "s"}` : "") +
        ` · ${op}`;
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    case "talker.alias": {
      const system = str(payload.system);
      const source = num(payload.source ?? payload.radio_id);
      const alias = str(payload.alias);
      const details = `radio ${source}: "${alias}"`;
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    case "cc.locked":
    case "cc.lost": {
      const system = str(payload.system);
      const freq = num(payload.frequency_hz);
      const details =
        freq ? `@ ${(freq / 1e6).toFixed(4)} MHz` : "";
      return { ts: ev.timestamp, kind: ev.kind, label, system, details, raw: ev.payload };
    }
    default:
      return null;
  }
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function num(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string") {
    const n = parseInt(v, 10);
    return Number.isFinite(n) ? n : 0;
  }
  return 0;
}

function formatTime(ts: string): string {
  try {
    return new Date(ts).toISOString().slice(11, 19);
  } catch {
    return ts.slice(11, 19);
  }
}
