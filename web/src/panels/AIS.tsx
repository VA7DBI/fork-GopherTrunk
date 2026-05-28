import { useEffect, useState } from "react";
import { fetchAISVessels, type AISMessage } from "../api/ais";
import { selectClientConfig, useShared } from "../store/shared";

// AIS panel — list of recent decoded marine-AIS messages. Each row
// shows the MMSI, the message-type tag, a short body summary, and
// either lat/lon + SOG/COG (position-bearing types: 1/2/3/4/18/19)
// or vessel-name / call-sign / destination (static types: 5/24).
//
// Polls /api/v1/ais/vessels every 5 s. Live SSE delivery via
// KindAISMessage bus event is a follow-up once peer-edit churn
// makes the poll cost visible.

const POLL_INTERVAL_MS = 5_000;

export function AIS() {
  const cfg = useShared(selectClientConfig);
  const [messages, setMessages] = useState<AISMessage[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const list = await fetchAISVessels(cfg, 200);
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

  return (
    <div className="space-y-3">
      <header className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">AIS</h2>
        <span className="text-xs text-muted">
          {messages.length} message{messages.length === 1 ? "" : "s"}
        </span>
      </header>

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
              <th className="text-left px-3 py-1 font-normal w-28">MMSI</th>
              <th className="text-left px-3 py-1 font-normal w-28">Type</th>
              <th className="text-left px-3 py-1 font-normal">Body</th>
              <th className="text-right px-3 py-1 font-normal w-32">Lat / Lon</th>
              <th className="text-right px-3 py-1 font-normal w-24">SOG / COG</th>
            </tr>
          </thead>
          <tbody>
            {messages.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-3 py-4 text-center text-muted">
                  No AIS messages yet. Add an{" "}
                  <code className="text-accent">ais.channels</code>{" "}
                  entry to your config (marine VHF 87B = 161.975 MHz · 88B =
                  162.025 MHz) and decoded vessels — position reports,
                  static + voyage data, base-station reports — will land
                  here as they arrive.
                </td>
              </tr>
            ) : (
              messages.map((m) => (
                <tr
                  key={m.id}
                  className={
                    "border-t border-border/60 " +
                    (m.fcs_ok ? "" : "bg-yellow-900/15")
                  }
                >
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(m.received_at)}
                  </td>
                  <td className="px-3 py-1 font-mono">
                    <span className="text-accent">{m.mmsi}</span>
                    {m.vessel_name && (
                      <div className="text-[10px] text-muted">
                        {m.vessel_name}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-1 uppercase">{m.type}</td>
                  <td className="px-3 py-1">
                    {m.body || <span className="text-muted">(no payload)</span>}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {m.has_position ? (
                      <span>
                        {m.latitude?.toFixed(4)}, {m.longitude?.toFixed(4)}
                      </span>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {m.has_position && (m.sog || m.cog) ? (
                      <span>
                        {(m.sog ?? 0).toFixed(1)}kn / {(m.cog ?? 0).toFixed(0)}°
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

function formatTime(ts: string): string {
  try {
    return new Date(ts).toISOString().slice(11, 19);
  } catch {
    return ts.slice(11, 19);
  }
}
