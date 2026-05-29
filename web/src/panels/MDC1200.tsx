import { useEffect, useState } from "react";
import { fetchMDC1200Messages, type MDC1200Message } from "../api/mdc1200";
import { selectClientConfig, useShared } from "../store/shared";

// MDC1200 panel — list of recent decoded Motorola FFSK signaling
// bursts off conventional analog voice channels. Each row shows the
// transmitting radio's unit ID, the decoded operation (PTT ID,
// emergency, status, radio check, ...) and whether the CRC validated.
// Emergency bursts are tinted red; CRC-failed bursts are dimmed.
//
// Polls /api/v1/mdc1200/messages every 5 s. Live SSE delivery via the
// KindMDC1200Message bus event is a follow-up.

const POLL_INTERVAL_MS = 5_000;

export function MDC1200() {
  const cfg = useShared(selectClientConfig);
  const [messages, setMessages] = useState<MDC1200Message[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const list = await fetchMDC1200Messages(cfg, 200);
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
        <h2 className="text-xl font-semibold">MDC1200</h2>
        <span className="text-xs text-muted">
          {messages.length} burst{messages.length === 1 ? "" : "s"}
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
              <th className="text-left px-3 py-1 font-normal w-24">Unit ID</th>
              <th className="text-left px-3 py-1 font-normal">Operation</th>
              <th className="text-left px-3 py-1 font-normal w-28">Op / Arg</th>
              <th className="text-left px-3 py-1 font-normal">Body</th>
              <th className="text-right px-3 py-1 font-normal w-16">CRC</th>
            </tr>
          </thead>
          <tbody>
            {messages.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-3 py-4 text-center text-muted">
                  No MDC1200 bursts yet. Add an{" "}
                  <code className="text-accent">mdc1200.channels</code> entry to
                  your config (a conventional analog VHF / UHF voice channel
                  carrying Motorola signaling) and decoded unit IDs, PTT ANI,
                  emergency / status / radio-check bursts will land here as
                  they arrive.
                </td>
              </tr>
            ) : (
              messages.map((m) => (
                <tr
                  key={m.id}
                  className={
                    "border-t border-border/60 " +
                    rowClass(m) +
                    (m.crc_ok ? "" : " opacity-60")
                  }
                >
                  <td className="px-3 py-1 font-mono text-muted">
                    {formatTime(m.received_at)}
                  </td>
                  <td className="px-3 py-1 font-mono text-accent">
                    {unitHex(m.unit_id)}
                  </td>
                  <td className="px-3 py-1">
                    {m.operation || <span className="text-muted">unknown</span>}
                  </td>
                  <td className="px-3 py-1 font-mono text-muted">
                    {hex2(m.op)} / {hex2(m.arg)}
                  </td>
                  <td className="px-3 py-1">
                    {m.body || <span className="text-muted">—</span>}
                  </td>
                  <td className="px-3 py-1 text-right font-mono">
                    {m.crc_ok ? (
                      <span className="text-emerald-400">ok</span>
                    ) : (
                      <span className="text-red-300">fail</span>
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

// rowClass tints emergency bursts red so they stand out.
function rowClass(m: MDC1200Message): string {
  if (m.operation === "Emergency") {
    return "bg-red-900/25";
  }
  return "";
}

function unitHex(id: number): string {
  return id.toString(16).toUpperCase().padStart(4, "0");
}

function hex2(v: number): string {
  return "0x" + v.toString(16).toUpperCase().padStart(2, "0");
}

function formatTime(ts: string): string {
  try {
    return new Date(ts).toISOString().slice(11, 19);
  } catch {
    return ts.slice(11, 19);
  }
}
