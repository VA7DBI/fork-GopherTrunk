import { useEffect, useMemo, useState } from "react";
import { fetchAPRSPackets, type APRSPacket } from "../api/aprs";
import { PositionMap, type MapPoint } from "../components/PositionMap";
import { selectClientConfig, useShared } from "../store/shared";

// APRS panel — list of recent decoded APRS / AX.25 packets. Each
// row shows the AX.25 envelope (src → dst + path), the decoded
// APRS sub-type (position / message / status / bulletin / ...) and
// a one-line summary body. Positions get a coordinate column;
// CRC-failed frames are highlighted yellow.
//
// Polls /api/v1/aprs/packets every 5 s. Live SSE delivery via
// KindAPRSPacket bus event is a follow-up once peer-edit churn
// makes the poll cost visible.

const POLL_INTERVAL_MS = 5_000;

export function APRS() {
  const cfg = useShared(selectClientConfig);
  const [packets, setPackets] = useState<APRSPacket[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const list = await fetchAPRSPackets(cfg, 200);
        if (cancel) return;
        setPackets(list);
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

  const mapPoints = useMemo<MapPoint[]>(
    () =>
      packets
        .filter(
          (p) =>
            typeof p.latitude === "number" &&
            typeof p.longitude === "number" &&
            !(p.latitude === 0 && p.longitude === 0),
        )
        .map((p) => ({
          id: `aprs-${p.id}`,
          latitude: p.latitude as number,
          longitude: p.longitude as number,
          kind: "aprs" as const,
          label: p.src,
          detail: `${p.type} · ${p.body ?? ""}`.trim(),
        })),
    [packets],
  );

  return (
    <div className="space-y-3">
      <header className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">APRS</h2>
        <span className="text-xs text-muted">
          {packets.length} packet{packets.length === 1 ? "" : "s"}
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
              <th className="text-left px-3 py-1 font-normal w-32">Src → Dst</th>
              <th className="text-left px-3 py-1 font-normal w-24">Type</th>
              <th className="text-left px-3 py-1 font-normal">Body</th>
              <th className="text-right px-3 py-1 font-normal w-32">Lat / Lon</th>
            </tr>
          </thead>
          <tbody>
            {packets.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-3 py-4 text-center text-muted">
                  No APRS packets yet. Add an{" "}
                  <code className="text-accent">aprs.channels</code>{" "}
                  entry to your config (NA primary: 144.39 MHz · EU R1:
                  144.800 MHz · JP: 144.64 MHz · ISS: 145.825 MHz) and
                  decoded packets — position beacons, messages,
                  bulletins, status, Mic-E — will land here as they
                  arrive.
                </td>
              </tr>
            ) : (
              packets.map((p) => (
                <tr
                  key={p.id}
                  className={
                    "border-t border-border/60 " +
                    (p.fcs_ok ? "" : "bg-yellow-900/15")
                  }
                >
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(p.received_at)}
                  </td>
                  <td className="px-3 py-1 font-mono">
                    <span className="text-accent">{p.src}</span>
                    <span className="text-muted"> → {p.dst}</span>
                    {p.path && (
                      <div className="text-[10px] text-muted">
                        via {p.path}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-1 uppercase">{p.type}</td>
                  <td className="px-3 py-1">
                    {p.body || <span className="text-muted">(no payload)</span>}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {p.latitude || p.longitude ? (
                      <span>
                        {p.latitude?.toFixed(4)}, {p.longitude?.toFixed(4)}
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
