import { useEffect, useState } from "react";
import { fetchAircraftReports, type AircraftReport } from "../api/adsb";
import { selectClientConfig, useShared } from "../store/shared";

// ADS-B panel — list of recent decoded Mode-S frames. Each row
// shows the ICAO 24-bit address (hex form, the standard "tail
// identifier" aviation people use), the kind (ident / airborne-
// pos / velocity / ...), a one-line body summary, and kind-
// specific data: callsign for identification rows, lat/lon +
// altitude for position rows, ground speed + track + vertical
// rate for velocity rows.
//
// Polls /api/v1/adsb/aircraft every 5 s. ADS-B is the highest-
// rate decoder (each aircraft sends 2-3 msg/s); the default
// limit shows the most recent 200, which is roughly 1 min of
// traffic on a busy channel.

const POLL_INTERVAL_MS = 5_000;

export function ADSB() {
  const cfg = useShared(selectClientConfig);
  const [reports, setReports] = useState<AircraftReport[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const list = await fetchAircraftReports(cfg, 200);
        if (cancel) return;
        setReports(list);
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
        <h2 className="text-xl font-semibold">ADS-B</h2>
        <span className="text-xs text-muted">
          {reports.length} message{reports.length === 1 ? "" : "s"}
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
              <th className="text-left px-3 py-1 font-normal w-24">ICAO</th>
              <th className="text-left px-3 py-1 font-normal w-28">Kind</th>
              <th className="text-left px-3 py-1 font-normal w-24">Callsign</th>
              <th className="text-right px-3 py-1 font-normal w-32">Lat / Lon</th>
              <th className="text-right px-3 py-1 font-normal w-24">Alt (ft)</th>
              <th className="text-right px-3 py-1 font-normal w-28">GS / Track</th>
              <th className="text-right px-3 py-1 font-normal w-24">VR (fpm)</th>
            </tr>
          </thead>
          <tbody>
            {reports.length === 0 ? (
              <tr>
                <td colSpan={8} className="px-3 py-4 text-center text-muted">
                  No ADS-B messages yet. Add an{" "}
                  <code className="text-accent">adsb.channels</code>{" "}
                  entry to your config (1090.000 MHz centre, 1 Msps
                  required — dedicate one RTL-SDR with a 1090 MHz
                  antenna to it) and decoded aircraft will land here
                  as they arrive. DSP wiring (1 Msps PPM
                  demodulation + Mode-S preamble detection + 56/112-
                  bit frame extraction) is the planned follow-up;
                  the protocol + storage + REST scaffolding is live
                  now.
                </td>
              </tr>
            ) : (
              reports.map((r) => (
                <tr
                  key={r.id}
                  className={
                    "border-t border-border/60 " +
                    (r.crc_valid ? "" : "bg-yellow-900/15")
                  }
                >
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(r.received_at)}
                  </td>
                  <td className="px-3 py-1 font-mono text-accent">
                    {r.icao_hex}
                  </td>
                  <td className="px-3 py-1 uppercase">{r.kind}</td>
                  <td className="px-3 py-1 font-mono">
                    {r.callsign || <span className="text-muted">—</span>}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {r.has_position && r.latitude !== undefined &&
                    r.longitude !== undefined ? (
                      <span>
                        {r.latitude.toFixed(4)}, {r.longitude.toFixed(4)}
                      </span>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {r.has_altitude && r.altitude_ft !== undefined ? (
                      r.altitude_ft.toLocaleString()
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {r.ground_speed_kn ? (
                      <span>
                        {r.ground_speed_kn}kn / {(r.track_deg ?? 0).toFixed(0)}°
                      </span>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {r.vertical_rate_fpm ? (
                      r.vertical_rate_fpm > 0
                        ? `+${r.vertical_rate_fpm}`
                        : String(r.vertical_rate_fpm)
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
