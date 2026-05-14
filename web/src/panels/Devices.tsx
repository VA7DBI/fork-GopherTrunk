import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import { Column, DataTable } from "../components/DataTable";
import { DetailField, DetailModal } from "../components/DetailModal";
import type { DeviceDTO } from "../api/types";
import { selectClientConfig, useShared } from "../store/shared";

const POLL_INTERVAL_MS = 5_000;

// Devices reflects the SDR pool. Live updates piggyback on the
// sdr.attached / sdr.detached event stream feeding the shared store,
// while this panel polls /api/v1/devices for the canonical snapshot.
export function Devices() {
  const cfg = useShared(selectClientConfig);
  const devices = useShared((s) => s.devices);
  const setDevices = useShared((s) => s.setDevices);
  const [selected, setSelected] = useState<DeviceDTO | null>(null);

  useEffect(() => {
    let cancel = false;
    const refresh = async () => {
      try {
        const data = await api.devices(cfg);
        if (!cancel) setDevices(data);
      } catch {
        // Keep the previous snapshot.
      }
    };
    refresh();
    const t = window.setInterval(refresh, POLL_INTERVAL_MS);
    return () => {
      cancel = true;
      window.clearInterval(t);
    };
  }, [cfg, setDevices]);

  const attachedCount = useMemo(
    () => devices.filter((d) => d.attached).length,
    [devices],
  );

  const columns: Column<DeviceDTO>[] = useMemo(
    () => [
      {
        key: "status",
        header: "",
        render: (r) =>
          r.attached ? (
            <span className="pill-ok" aria-label="attached">●</span>
          ) : (
            <span className="pill-err" aria-label="detached">○</span>
          ),
      },
      {
        key: "serial",
        header: "Serial",
        render: (r) => <span className="font-mono text-accent">{r.serial}</span>,
        sort: (a, b) => a.serial.localeCompare(b.serial),
      },
      {
        key: "driver",
        header: "Driver",
        render: (r) => <span className="text-xs">{r.driver}</span>,
        sort: (a, b) => a.driver.localeCompare(b.driver),
      },
      {
        key: "tuner",
        header: "Tuner",
        render: (r) => <span className="text-xs">{r.tuner ?? "—"}</span>,
        sort: (a, b) => (a.tuner ?? "").localeCompare(b.tuner ?? ""),
        className: "hidden md:table-cell",
        headerClassName: "hidden md:table-cell",
      },
      {
        key: "role",
        header: "Role",
        render: (r) => (
          <span className="text-xs uppercase tracking-wider text-muted">
            {r.role ?? "unassigned"}
          </span>
        ),
        sort: (a, b) => (a.role ?? "").localeCompare(b.role ?? ""),
      },
    ],
    [],
  );

  return (
    <div className="space-y-3">
      <header className="flex items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">Devices</h2>
        <span className="text-xs text-muted">
          {attachedCount} of {devices.length} attached
        </span>
      </header>

      <DataTable
        rows={devices}
        columns={columns}
        rowKey={(r) => r.serial}
        defaultSortKey="serial"
        onRowClick={(r) => setSelected(r)}
        emptyMessage="No SDRs known to the daemon. Check udev rules / WinUSB driver / hardware connection."
      />

      {selected && (
        <DetailModal
          title={selected.serial}
          subtitle={`${selected.driver}${selected.tuner ? " · " + selected.tuner : ""}`}
          onClose={() => setSelected(null)}
        >
          <div className="grid grid-cols-2 gap-3">
            <DetailField
              label="Status"
              value={selected.attached ? "attached" : "detached"}
            />
            <DetailField label="Role" value={selected.role} />
            <DetailField label="Gain" mono value={selected.gain} />
            <DetailField
              label="PPM"
              mono
              value={selected.ppm != null ? `${selected.ppm}` : null}
            />
            <DetailField
              label="Bias-T"
              value={
                selected.bias_tee == null
                  ? null
                  : selected.bias_tee
                    ? "on"
                    : "off"
              }
            />
          </div>
        </DetailModal>
      )}
    </div>
  );
}
