import { useEffect, useMemo, useState } from "react";
import { fetchDSCMessages, type DSCMessage } from "../api/dsc";
import { PositionMap, type MapPoint } from "../components/PositionMap";
import { selectClientConfig, useShared } from "../store/shared";

// DSC panel — list of recent decoded marine DSC sequences. The
// row colour reflects the category: distress = red, urgency =
// orange, safety = blue, routine = default. Distress alerts also
// surface nature ("fire / explosion", "sinking", etc.) and (when
// the alert included one) a position.
//
// Polls /api/v1/dsc/messages every 5 s. Live SSE delivery via
// KindDSCMessage bus event is a follow-up once peer-edit churn
// makes the poll cost visible.

const POLL_INTERVAL_MS = 5_000;

export function DSC() {
  const cfg = useShared(selectClientConfig);
  const [messages, setMessages] = useState<DSCMessage[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const list = await fetchDSCMessages(cfg, 200);
        if (cancel) return;
        setMessages(list);
        setError(null);
      } catch (e) {
        if (cancel) return;
        setError(e instanceof Error ? e.message : String(e));
      }
    };
    refresh();
    const t = window.setInterval(refresh, POLL_INTERVAL_MS);
    return () => {
      cancel = true;
      window.clearInterval(t);
    };
  }, [cfg]);

  // DSC distress alerts are the only DSC sequence type that
  // commonly carries a position; surface those alerts on the
  // shared map with the high-visibility red marker so the
  // operator's eye is drawn to them immediately.
  const mapPoints = useMemo<MapPoint[]>(
    () =>
      messages
        .filter(
          (m) =>
            m.has_position &&
            typeof m.latitude === "number" &&
            typeof m.longitude === "number",
        )
        .map((m) => ({
          id: `dsc-${m.id}`,
          latitude: m.latitude as number,
          longitude: m.longitude as number,
          kind: "dsc-distress" as const,
          label: `MMSI ${String(m.self_mmsi).padStart(9, "0")}`,
          detail: m.nature || m.format,
        })),
    [messages],
  );

  return (
    <div className="space-y-3">
      <header className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">DSC</h2>
        <span className="text-xs text-muted">
          {messages.length} sequence{messages.length === 1 ? "" : "s"}
        </span>
      </header>

      {mapPoints.length > 0 && <PositionMap points={mapPoints} />}

      {error && (
        <div className="rounded border border-red-700/40 bg-red-900/20 text-red-200 text-xs px-3 py-2">
          {error}
        </div>
      )}

      <div className="rounded border border-border overflow-hidden">
        <table className="w-full text-xs">
          <thead className="bg-surface text-muted">
            <tr>
              <th className="text-left px-3 py-1 font-normal w-24">Received</th>
              <th className="text-left px-3 py-1 font-normal w-24">Format</th>
              <th className="text-left px-3 py-1 font-normal w-24">Category</th>
              <th className="text-left px-3 py-1 font-normal w-28">Self MMSI</th>
              <th className="text-left px-3 py-1 font-normal w-28">Target / Nature</th>
              <th className="text-left px-3 py-1 font-normal">Body</th>
              <th className="text-right px-3 py-1 font-normal w-32">Lat / Lon</th>
            </tr>
          </thead>
          <tbody>
            {messages.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-3 py-4 text-center text-muted">
                  No DSC sequences yet. Add a{" "}
                  <code className="text-accent">dsc.channels</code>{" "}
                  entry to your config (marine VHF channel 70 =
                  156.525 MHz · HF 2.187.5 / 8.414.5 / 12.577 /
                  16.804.5 kHz) and decoded distress / safety /
                  routine calls will land here as they arrive. DSP
                  wiring (1200 Bd FSK at 1300 / 2100 Hz tones →
                  10-bit symbol assembly → BCH check + DX/RX
                  redundancy → message parser) is the planned
                  follow-up; the protocol + storage + REST
                  scaffolding is live now.
                </td>
              </tr>
            ) : (
              messages.map((m) => (
                <tr
                  key={m.id}
                  className={
                    "border-t border-border/60 " + categoryRowClass(m.category)
                  }
                >
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(m.received_at)}
                  </td>
                  <td className="px-3 py-1 uppercase">{m.format}</td>
                  <td className="px-3 py-1 uppercase">{m.category}</td>
                  <td className="px-3 py-1 font-mono text-accent">
                    {String(m.self_mmsi).padStart(9, "0")}
                  </td>
                  <td className="px-3 py-1 font-mono">
                    {m.target_mmsi
                      ? String(m.target_mmsi).padStart(9, "0")
                      : m.nature || <span className="text-muted">—</span>}
                  </td>
                  <td className="px-3 py-1">
                    {m.body || <span className="text-muted">(no payload)</span>}
                    {m.time_utc && (
                      <div className="text-[10px] text-muted">
                        UTC {m.time_utc}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {m.has_position && m.latitude !== undefined &&
                    m.longitude !== undefined ? (
                      <span>
                        {m.latitude.toFixed(4)}, {m.longitude.toFixed(4)}
                      </span>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// categoryRowClass tints the row by DSC category — distress is
// red, urgency orange, safety blue. Routine traffic gets no tint.
function categoryRowClass(category: string): string {
  switch (category) {
    case "distress":
      return "bg-red-900/25";
    case "urgency":
      return "bg-orange-900/20";
    case "safety":
      return "bg-blue-900/15";
  }
  return "";
}

function formatTime(ts: string): string {
  try {
    return new Date(ts).toISOString().slice(11, 19);
  } catch {
    return ts.slice(11, 19);
  }
}
