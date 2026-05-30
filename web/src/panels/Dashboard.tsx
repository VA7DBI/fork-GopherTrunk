import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { PlutoRuntimeDTO, RuntimeDTO } from "../api/types";
import { selectClientConfig, useShared } from "../store/shared";

const PLUTO_RECENT_WINDOW_MS = 10 * 60 * 1000;

// Dashboard: top-line counts + a peek at the last few events. The
// fuller dashboard (with charts) is a follow-up PR; this one
// proves the live-update wiring works end-to-end.
export function Dashboard() {
  const cfg = useShared(selectClientConfig);
  const setHealth = useShared((s) => s.setHealth);
  const setActiveCalls = useShared((s) => s.setActiveCalls);
  const setDevices = useShared((s) => s.setDevices);
  const setAudio = useShared((s) => s.setAudio);

  const health = useShared((s) => s.health);
  const activeCalls = useShared((s) => s.activeCalls);
  const devices = useShared((s) => s.devices);
  const audio = useShared((s) => s.audio);
  const events = useShared((s) => s.events);
  const wsStatus = useShared((s) => s.wsStatus);
  const [runtime, setRuntime] = useState<RuntimeDTO | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const [h, calls, devs, aud, rt] = await Promise.allSettled([
          api.health(cfg),
          api.activeCalls(cfg),
          api.devices(cfg),
          api.audio(cfg),
          api.runtime(cfg),
        ]);
        if (cancel) return;
        if (h.status === "fulfilled") setHealth(h.value);
        if (calls.status === "fulfilled") setActiveCalls(calls.value);
        if (devs.status === "fulfilled") setDevices(devs.value);
        if (aud.status === "fulfilled") setAudio(aud.value);
        if (rt.status === "fulfilled") setRuntime(rt.value);
      } catch {
        // Silent: errors land on the toast strip from elsewhere.
      }
    };
    refresh();
    const timer = window.setInterval(refresh, 3_000);
    return () => {
      cancel = true;
      window.clearInterval(timer);
    };
  }, [cfg, setHealth, setActiveCalls, setDevices, setAudio]);

  return (
    <div className="space-y-4">
      <header className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Dashboard</h2>
        <span
          className={
            wsStatus === "open"
              ? "pill-ok"
              : wsStatus === "connecting"
                ? "pill-warn"
                : "pill-err"
          }
        >
          {wsStatus === "open"
            ? "Live"
            : wsStatus === "connecting"
              ? "Connecting"
              : "Offline"}
        </span>
      </header>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard label="Active calls" value={activeCalls.length} />
        <StatCard label="Devices attached" value={devices.length} />
        <StatCard
          label="Audio backend"
          value={
            audio
              ? audio.backend_enabled
                ? `${(audio.sample_rate / 1000).toFixed(1)} kHz`
                : "off"
              : "—"
          }
        />
        <StatCard
          label="Daemon"
          value={health?.status ?? "unknown"}
          ok={health?.status === "ok"}
        />
      </div>

      {showPlutoDashboard(runtime) && (
        <section className="panel p-4">
          <div className="flex items-center gap-2 mb-2">
            <h3 className="panel-title">Pluto Plus health</h3>
            <span className={plutoSeverity(runtime?.pluto_runtime).className}>
              {plutoSeverity(runtime?.pluto_runtime).label}
            </span>
          </div>
          <p className="text-sm">
            Reconnects <span className="font-mono">{runtime?.pluto_runtime?.reconnects ?? 0}</span>
            {"  ·  "}
            Failures <span className="font-mono">{plutoFailureTotal(runtime?.pluto_runtime)}</span>
          </p>
          {plutoFailureBreakdown(runtime?.pluto_runtime) && (
            <p className="text-xs text-muted mt-1">{plutoFailureBreakdown(runtime?.pluto_runtime)}</p>
          )}
          {plutoRemediationHint(runtime?.pluto_runtime) && (
            <p className="text-xs text-muted mt-1">
              hint: {plutoRemediationHint(runtime?.pluto_runtime)}
            </p>
          )}
        </section>
      )}

      <section className="panel p-4">
        <h3 className="panel-title mb-2">Recent events</h3>
        {events.length === 0 ? (
          <p className="text-muted text-sm">
            No events yet. The stream connects automatically.
          </p>
        ) : (
          <ul className="space-y-1 text-xs font-mono max-h-72 overflow-auto">
            {events
              .slice(-50)
              .reverse()
              .map((ev, i) => (
                <li key={`${ev.timestamp}-${i}`} className="flex gap-3">
                  <span className="text-muted shrink-0">
                    {ev.timestamp.replace("T", " ").replace(/\..*$/, "")}
                  </span>
                  <span className="text-accent shrink-0">{ev.kind}</span>
                </li>
              ))}
          </ul>
        )}
      </section>

      <section className="panel p-4">
        <h3 className="panel-title mb-2">Active calls</h3>
        {activeCalls.length === 0 ? (
          <p className="text-muted text-sm">No calls right now.</p>
        ) : (
          <ul className="divide-y divide-panel">
            {activeCalls.map((c) => (
              <li
                key={`${c.device_serial}-${c.started_at}`}
                className="py-2 flex items-baseline gap-3"
              >
                <span className="font-mono text-accent">
                  TG {c.grant.group_id}
                </span>
                <span className="text-sm">
                  {c.talkgroup?.alpha_tag ?? c.grant.system}
                </span>
                <span className="ml-auto text-xs text-muted">
                  {c.device_serial}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function showPlutoDashboard(runtime: RuntimeDTO | null): boolean {
  const backends = Array.isArray(runtime?.sdr_backends) ? runtime.sdr_backends : [];
  if (backends.some((value) => String(value).toLowerCase() === "plutoplus")) {
    return true;
  }
  const pluto = runtime?.pluto_runtime;
  return (pluto?.reconnects ?? 0) > 0 || plutoFailureTotal(pluto) > 0;
}

function plutoFailureTotal(pluto?: PlutoRuntimeDTO): number {
  return (
    (pluto?.reconnect_failures ?? 0) +
    (pluto?.dial_failures ?? 0) +
    (pluto?.handshake_failures ?? 0) +
    (pluto?.command_failures ?? 0) +
    (pluto?.stream_failures ?? 0) +
    (pluto?.unknown_failures ?? 0)
  );
}

function plutoFailureBreakdown(pluto?: PlutoRuntimeDTO): string {
  if (!pluto) return "";
  const parts: string[] = [];
  if ((pluto.dial_failures ?? 0) > 0) parts.push(`dial ${pluto.dial_failures}`);
  if ((pluto.handshake_failures ?? 0) > 0) parts.push(`handshake ${pluto.handshake_failures}`);
  if ((pluto.command_failures ?? 0) > 0) parts.push(`command ${pluto.command_failures}`);
  if ((pluto.stream_failures ?? 0) > 0) parts.push(`stream ${pluto.stream_failures}`);
  if ((pluto.unknown_failures ?? 0) > 0) parts.push(`unknown ${pluto.unknown_failures}`);
  return parts.join("  ·  ");
}

function plutoSeverity(pluto?: PlutoRuntimeDTO): { label: string; className: string } {
  const failures = plutoFailureTotal(pluto);
  const recent = plutoFailuresRecent(pluto);
  switch (true) {
    case failures >= 5 && recent:
      return { label: "unstable", className: "pill-err" };
    case (failures > 0 && recent) || ((pluto?.reconnects ?? 0) >= 3 && recent):
      return { label: "degraded", className: "pill-warn" };
    case failures > 0:
      return { label: "historical", className: "pill-ok" };
    default:
      return { label: "stable", className: "pill-ok" };
  }
}

function plutoRemediationHint(pluto?: PlutoRuntimeDTO): string {
  if (!plutoFailuresRecent(pluto)) return "";
  const [stage, count] = plutoDominantFailure(pluto);
  if (count === 0) return "";
  switch (stage) {
    case "dial":
      return "check Pluto endpoint address/USB transport and device power";
    case "handshake":
      return "verify RTL-TCP compatibility and firmware behavior on connect";
    case "command":
      return "inspect tuner command sequence and Pluto command responses";
    case "stream":
      return "check USB/network stability and host performance under load";
    default:
      return "inspect daemon logs for plutoplus transport error details";
  }
}

function plutoDominantFailure(pluto?: PlutoRuntimeDTO): [string, number] {
  const stages: Array<[string, number]> = [
    ["dial", pluto?.dial_failures ?? 0],
    ["handshake", pluto?.handshake_failures ?? 0],
    ["command", pluto?.command_failures ?? 0],
    ["stream", pluto?.stream_failures ?? 0],
    ["unknown", pluto?.unknown_failures ?? 0],
  ];
  let maxStage = "";
  let maxCount = 0;
  for (const [stage, count] of stages) {
    if (count > maxCount) {
      maxStage = stage;
      maxCount = count;
    }
  }
  return [maxStage, maxCount];
}

function plutoFailuresRecent(pluto?: PlutoRuntimeDTO): boolean {
  if (!pluto) return false;
  const lastFailureAt = pluto.last_failure_at;
  if (!lastFailureAt) {
    return plutoFailureTotal(pluto) > 0;
  }
  const parsed = Date.parse(lastFailureAt);
  if (Number.isNaN(parsed)) {
    return plutoFailureTotal(pluto) > 0;
  }
  return Date.now() - parsed <= PLUTO_RECENT_WINDOW_MS;
}

function StatCard({
  label,
  value,
  ok,
}: {
  label: string;
  value: string | number;
  ok?: boolean;
}) {
  return (
    <div className="panel p-4">
      <p className="panel-title">{label}</p>
      <p
        className={
          ok === false ? "stat-value text-err" : "stat-value"
        }
      >
        {value}
      </p>
    </div>
  );
}
