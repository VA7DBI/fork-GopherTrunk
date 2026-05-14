import { useMemo, useState } from "react";
import { Column, DataTable } from "../components/DataTable";
import type { EventDTO } from "../api/types";
import { useShared } from "../store/shared";

// Events renders the live ring buffer the WebSocket stream populates
// into the shared store. No polling — the stream pushes everything;
// see App.tsx's openEventStream wire-up.
export function Events() {
  const events = useShared((s) => s.events);
  const wsStatus = useShared((s) => s.wsStatus);
  const eventCap = useShared((s) => s.eventCap);
  const [filter, setFilter] = useState("");
  const [paused, setPaused] = useState(false);
  const [snapshot, setSnapshot] = useState<EventDTO[] | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  // When paused, freeze the table at the snapshot the user paused on;
  // when running, mirror the live store ring.
  const visible = paused ? (snapshot ?? events) : events;

  const filtered = useMemo(() => {
    if (!filter.trim()) return visible;
    const needle = filter.toLowerCase();
    return visible.filter(
      (e) =>
        e.kind.toLowerCase().includes(needle) ||
        e.timestamp.toLowerCase().includes(needle) ||
        JSON.stringify(e.payload ?? "").toLowerCase().includes(needle),
    );
  }, [visible, filter]);

  // Newest events first — reverse so the latest is at the top, the
  // mobile-friendly read order.
  const ordered = useMemo(() => filtered.slice().reverse(), [filtered]);

  const columns: Column<EventDTO>[] = useMemo(
    () => [
      {
        key: "ts",
        header: "Time",
        render: (r) => (
          <span className="font-mono text-xs text-muted whitespace-nowrap">
            {r.timestamp.replace("T", " ").replace(/\..*$/, "")}
          </span>
        ),
        sort: (a, b) => a.timestamp.localeCompare(b.timestamp),
      },
      {
        key: "kind",
        header: "Kind",
        render: (r) => (
          <span className="font-mono text-accent">{r.kind}</span>
        ),
        sort: (a, b) => a.kind.localeCompare(b.kind),
      },
      {
        key: "preview",
        header: "Payload",
        render: (r) => (
          <span className="text-xs font-mono text-muted truncate inline-block max-w-[28ch] sm:max-w-[60ch]">
            {previewPayload(r.payload)}
          </span>
        ),
      },
    ],
    [],
  );

  function togglePause() {
    if (paused) {
      setPaused(false);
      setSnapshot(null);
    } else {
      setSnapshot(events.slice());
      setPaused(true);
    }
  }

  return (
    <div className="space-y-3">
      <header className="flex items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">Events</h2>
        <div className="flex items-center gap-2 text-xs">
          <span
            className={
              wsStatus === "open"
                ? "pill-ok"
                : wsStatus === "connecting"
                  ? "pill-warn"
                  : "pill-err"
            }
          >
            {wsStatus}
          </span>
          <span className="text-muted">
            {events.length}/{eventCap}
          </span>
        </div>
      </header>

      <div className="flex flex-wrap gap-2">
        <input
          type="search"
          className="input flex-1 sm:max-w-md"
          placeholder="Filter by kind, timestamp, or payload…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          aria-label="Filter events"
        />
        <button
          className={paused ? "btn-primary" : "btn-ghost"}
          onClick={togglePause}
          aria-pressed={paused}
        >
          {paused ? "Resume" : "Pause"}
        </button>
      </div>

      <DataTable
        rows={ordered}
        columns={columns}
        rowKey={(r, i) => `${r.timestamp}-${r.kind}-${i}`}
        onRowClick={(_r, key) =>
          setExpanded((prev) => (prev === key ? null : key))
        }
        renderExpansion={(r) => (
          <pre className="whitespace-pre-wrap break-words font-mono text-xs text-fg/80">
            {JSON.stringify(r, null, 2)}
          </pre>
        )}
        expandedKey={expanded}
        emptyMessage={
          events.length === 0
            ? "No events yet — the WebSocket stream pushes new ones live."
            : "No events match the filter."
        }
      />
    </div>
  );
}

function previewPayload(p: unknown): string {
  if (p == null) return "";
  try {
    const s = JSON.stringify(p);
    return s.length > 120 ? `${s.slice(0, 117)}…` : s;
  } catch {
    return String(p);
  }
}
