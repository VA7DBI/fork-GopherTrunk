import { useEffect, useMemo, useRef, useState } from "react";
import {
  fetchSpectrumDevices,
  type SpectrumDevice,
} from "../api/spectrum";
import {
  openIQStream,
  type IQFrame,
  type IQPoint,
} from "../api/diag";
import { selectClientConfig, useShared } from "../store/shared";

// Constellation panel — 2D scatter of decimated IQ samples. Useful
// for spotting the symbol-domain shape of whatever the SDR is tuned
// to without launching a separate SDR receiver.
//
//   - DC bias / unmodulated carrier   →  one bright dot off-centre
//   - PSK / QPSK                       →  two or four clusters at
//                                         ±0.5+0i etc.
//   - C4FM / FSK                       →  two or four arcs
//   - AM voice                          →  rotating cluster, modulated
//                                         in amplitude
//   - Wideband noise                    →  diffuse circle around the
//                                         origin
//
// 2 ksps decimated stream → ~50 ms / frame at 4 chunks per frame → a
// canvas redraw every 50 ms is responsive without burning CPU.

const TARGET_RATE_SPS = 2000;
const POINT_BUFFER = 2000;       // how many recent points to render
const CANVAS_PX = 360;           // square canvas; CSS scales to width
const AXIS_PADDING = 14;

type ConnState = "connecting" | "open" | "closed";

export function Constellation() {
  const cfg = useShared(selectClientConfig);
  const [devices, setDevices] = useState<SpectrumDevice[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [conn, setConn] = useState<ConnState>("closed");
  const [latest, setLatest] = useState<IQFrame | null>(null);
  const [error, setError] = useState<string | null>(null);

  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const bufferRef = useRef<IQPoint[]>([]);

  // Reuse the spectrum devices endpoint — same broker pool.
  useEffect(() => {
    let cancel = false;
    (async () => {
      try {
        const list = await fetchSpectrumDevices(cfg);
        if (cancel) return;
        setDevices(list);
        setError(null);
        if (list.length > 0 && selected == null) setSelected(list[0].serial);
      } catch (e) {
        if (cancel) return;
        setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancel = true;
    };
  }, [cfg, selected]);

  useEffect(() => {
    if (!selected) return;
    bufferRef.current = [];
    setLatest(null);

    const stream = openIQStream(cfg, {
      serial: selected,
      rate: TARGET_RATE_SPS,
      onFrame: (f) => {
        setLatest(f);
        const buf = bufferRef.current;
        for (const p of f.points) buf.push(p);
        if (buf.length > POINT_BUFFER) {
          bufferRef.current = buf.slice(buf.length - POINT_BUFFER);
        }
        renderConstellation(canvasRef.current, bufferRef.current);
      },
      onStatus: setConn,
    });
    return () => stream.close();
  }, [cfg, selected]);

  const tuningLabel = useMemo(() => {
    if (!latest) return "";
    return `${(latest.center_hz / 1e6).toFixed(4)} MHz · ${latest.sample_rate} sps · ${latest.energy_dbfs.toFixed(1)} dBFS`;
  }, [latest]);

  return (
    <div className="space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">Constellation</h2>
        <div className="flex items-center gap-2 text-xs">
          <span className="text-muted">SDR:</span>
          <select
            className="bg-surface border border-border rounded px-2 py-1"
            value={selected ?? ""}
            onChange={(e) => setSelected(e.target.value || null)}
            disabled={devices.length === 0}
          >
            {devices.length === 0 && <option value="">No SDRs available</option>}
            {devices.map((d) => (
              <option key={d.serial} value={d.serial}>
                {d.serial} · {d.role}
              </option>
            ))}
          </select>
          <ConnPill state={conn} />
        </div>
      </header>

      {error && (
        <div className="rounded border border-red-700/40 bg-red-900/20 text-red-200 text-xs px-3 py-2">
          {error}
        </div>
      )}

      <div className="font-mono text-xs text-muted">{tuningLabel || "—"}</div>

      <div className="rounded border border-border bg-black flex items-center justify-center">
        <canvas
          ref={canvasRef}
          width={CANVAS_PX}
          height={CANVAS_PX}
          className="block"
          style={{ width: CANVAS_PX, height: CANVAS_PX, imageRendering: "pixelated" }}
          aria-label="IQ constellation scatter"
        />
      </div>

      <div className="text-[11px] text-muted">
        Plots decimated IQ samples ({TARGET_RATE_SPS} sps). Bright = recent.
        Clusters at ±0.5+0i suggest PSK; concentric arcs suggest FSK;
        a diffuse circle is wideband noise.
      </div>
    </div>
  );
}

function ConnPill({ state }: { state: ConnState }) {
  if (state === "open") return <span className="pill-ok">live</span>;
  if (state === "connecting") return <span className="pill-warn">connecting</span>;
  return <span className="pill-err">offline</span>;
}

function renderConstellation(canvas: HTMLCanvasElement | null, points: IQPoint[]) {
  if (!canvas) return;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const w = canvas.width;
  const h = canvas.height;

  // Background.
  ctx.fillStyle = "rgb(0, 0, 0)";
  ctx.fillRect(0, 0, w, h);

  // Axes.
  ctx.strokeStyle = "rgba(120, 120, 140, 0.35)";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(w / 2, AXIS_PADDING);
  ctx.lineTo(w / 2, h - AXIS_PADDING);
  ctx.moveTo(AXIS_PADDING, h / 2);
  ctx.lineTo(w - AXIS_PADDING, h / 2);
  ctx.stroke();

  // Reference rings at |z| = 0.5 and |z| = 1.0.
  ctx.strokeStyle = "rgba(120, 120, 140, 0.2)";
  ctx.beginPath();
  ctx.arc(w / 2, h / 2, (w / 2 - AXIS_PADDING) * 0.5, 0, Math.PI * 2);
  ctx.stroke();
  ctx.beginPath();
  ctx.arc(w / 2, h / 2, (w / 2 - AXIS_PADDING) * 1.0, 0, Math.PI * 2);
  ctx.stroke();

  // Plot points with fade-from-old. Older indices in the buffer are
  // older samples; render them dimmer.
  const n = points.length;
  if (n === 0) return;
  const half = (w / 2 - AXIS_PADDING);
  for (let idx = 0; idx < n; idx++) {
    const p = points[idx];
    // Map |i|, |q| of 1.0 to half the canvas (so the unit circle
    // touches the rim).
    const x = w / 2 + p.i * half;
    const y = h / 2 - p.q * half; // canvas Y grows downward
    // Fade: oldest 25% are dim, newest 25% are bright.
    const age = idx / n; // 0..1
    const alpha = 0.15 + 0.85 * age * age;
    ctx.fillStyle = `rgba(120, 210, 255, ${alpha})`;
    ctx.fillRect(x - 1, y - 1, 2, 2);
  }
}
