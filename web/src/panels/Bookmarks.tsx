import { useEffect, useMemo, useState } from "react";
import {
  bookmarks as bookmarksAPI,
  type Bookmark,
  type BookmarkInput,
} from "../api/bookmarks";
import { selectClientConfig, useShared } from "../store/shared";

// Bookmarks panel — operator-managed conventional channel list.
// Backed by the SQLite bookmarks table on the daemon side. UI here
// is intentionally compact: a table of bookmarks grouped by the
// operator-defined "group" tag, an inline create/edit form, and a
// delete button per row. Live mutations re-fetch the list.
//
// Click-to-tune integration with the Spectrum panel can be added
// once an external retune REST endpoint lands; for now the panel is
// the single source of truth for the bookmark list.

const EMPTY_DRAFT: BookmarkInput = {
  name: "",
  freq_hz: 0,
  mode: "FM",
  ctcss_hz: 0,
  dcs_code: 0,
  notes: "",
  group: "",
};

type Draft = BookmarkInput & { id?: number };

export function Bookmarks() {
  const cfg = useShared(selectClientConfig);
  const [rows, setRows] = useState<Bookmark[]>([]);
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = async () => {
    try {
      const list = await bookmarksAPI.list(cfg);
      setRows(list);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    refresh();
    // Bookmarks rarely change — poll on a long interval just to
    // catch peer edits without complicating SSE wiring for v1.
    const t = window.setInterval(refresh, 30_000);
    return () => window.clearInterval(t);
    // refresh closes over cfg; rerun only when the daemon connection changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg]);

  const grouped = useMemo(() => {
    const byGroup = new Map<string, Bookmark[]>();
    for (const r of rows) {
      const key = r.group || "Ungrouped";
      const list = byGroup.get(key) ?? [];
      list.push(r);
      byGroup.set(key, list);
    }
    return Array.from(byGroup.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [rows]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!draft.name.trim() || draft.freq_hz <= 0) {
      setError("Name and frequency are required.");
      return;
    }
    setLoading(true);
    try {
      const payload: BookmarkInput = {
        name: draft.name.trim(),
        freq_hz: draft.freq_hz,
        mode: draft.mode || "FM",
        ctcss_hz: draft.ctcss_hz || 0,
        dcs_code: draft.dcs_code || 0,
        notes: draft.notes ?? "",
        group: draft.group ?? "",
      };
      if (draft.id) {
        await bookmarksAPI.update(cfg, draft.id, payload);
      } else {
        await bookmarksAPI.create(cfg, payload);
      }
      setDraft(EMPTY_DRAFT);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  const remove = async (id: number) => {
    setLoading(true);
    try {
      await bookmarksAPI.remove(cfg, id);
      if (draft.id === id) setDraft(EMPTY_DRAFT);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  const startEdit = (b: Bookmark) => {
    setDraft({
      id: b.id,
      name: b.name,
      freq_hz: b.freq_hz,
      mode: b.mode,
      ctcss_hz: b.ctcss_hz ?? 0,
      dcs_code: b.dcs_code ?? 0,
      notes: b.notes ?? "",
      group: b.group ?? "",
    });
  };

  return (
    <div className="space-y-4">
      <header className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Bookmarks</h2>
        <span className="text-xs text-muted">
          {rows.length} channel{rows.length === 1 ? "" : "s"}
        </span>
      </header>

      {error && (
        <div className="rounded border border-red-700/40 bg-red-900/20 text-red-200 text-xs px-3 py-2">
          {error}
        </div>
      )}

      <form
        onSubmit={submit}
        className="rounded border border-border bg-surface p-3 space-y-2"
      >
        <h3 className="font-medium text-sm">
          {draft.id ? "Edit bookmark" : "New bookmark"}
        </h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-2">
          <label className="text-xs space-y-1">
            <span className="text-muted">Name</span>
            <input
              type="text"
              className="block w-full bg-bg border border-border rounded px-2 py-1"
              value={draft.name}
              onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              placeholder="Marine Ch 16"
              required
            />
          </label>
          <label className="text-xs space-y-1">
            <span className="text-muted">Frequency (Hz)</span>
            <input
              type="number"
              className="block w-full bg-bg border border-border rounded px-2 py-1 font-mono"
              value={draft.freq_hz || ""}
              onChange={(e) =>
                setDraft({ ...draft, freq_hz: Number(e.target.value) })
              }
              placeholder="156800000"
              required
            />
          </label>
          <label className="text-xs space-y-1">
            <span className="text-muted">Mode</span>
            <select
              className="block w-full bg-bg border border-border rounded px-2 py-1"
              value={draft.mode}
              onChange={(e) => setDraft({ ...draft, mode: e.target.value })}
            >
              <option value="FM">FM</option>
              <option value="NFM">NFM</option>
              <option value="AM">AM</option>
              <option value="USB">USB</option>
              <option value="LSB">LSB</option>
              <option value="CW">CW</option>
              <option value="DMR">DMR</option>
              <option value="P25">P25</option>
            </select>
          </label>
          <label className="text-xs space-y-1">
            <span className="text-muted">Group</span>
            <input
              type="text"
              className="block w-full bg-bg border border-border rounded px-2 py-1"
              value={draft.group}
              onChange={(e) => setDraft({ ...draft, group: e.target.value })}
              placeholder="marine / ham-2m / utility"
            />
          </label>
          <label className="text-xs space-y-1 sm:col-span-2 lg:col-span-4">
            <span className="text-muted">Notes</span>
            <input
              type="text"
              className="block w-full bg-bg border border-border rounded px-2 py-1"
              value={draft.notes ?? ""}
              onChange={(e) => setDraft({ ...draft, notes: e.target.value })}
              placeholder="International distress (NMEA)"
            />
          </label>
        </div>
        <div className="flex gap-2">
          <button
            type="submit"
            disabled={loading}
            className="px-3 py-1 rounded bg-accent text-bg text-xs font-medium disabled:opacity-50"
          >
            {draft.id ? "Save edits" : "Add bookmark"}
          </button>
          {draft.id && (
            <button
              type="button"
              className="px-3 py-1 rounded border border-border text-xs"
              onClick={() => setDraft(EMPTY_DRAFT)}
            >
              Cancel
            </button>
          )}
        </div>
      </form>

      {grouped.length === 0 ? (
        <div className="text-xs text-muted py-4">
          No bookmarks yet. Add one above — typical use is to bookmark
          marine VHF Ch 16, NOAA weather channels, FRS/GMRS, repeater
          outputs, and the local public-safety conventional fall-backs.
        </div>
      ) : (
        <div className="space-y-3">
          {grouped.map(([group, list]) => (
            <section key={group} className="rounded border border-border">
              <header className="px-3 py-2 border-b border-border bg-surface text-xs uppercase tracking-wider text-muted">
                {group}
              </header>
              <table className="w-full text-xs">
                <thead className="text-muted">
                  <tr>
                    <th className="text-left px-3 py-1 font-normal">Name</th>
                    <th className="text-right px-3 py-1 font-normal">
                      Freq (MHz)
                    </th>
                    <th className="text-left px-3 py-1 font-normal">Mode</th>
                    <th className="text-left px-3 py-1 font-normal hidden md:table-cell">
                      Notes
                    </th>
                    <th className="text-right px-3 py-1 font-normal">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((b) => (
                    <tr key={b.id} className="border-t border-border/60">
                      <td className="px-3 py-1">{b.name}</td>
                      <td className="px-3 py-1 text-right font-mono text-accent">
                        {(b.freq_hz / 1e6).toFixed(4)}
                      </td>
                      <td className="px-3 py-1">{b.mode}</td>
                      <td className="px-3 py-1 hidden md:table-cell text-muted">
                        {b.notes}
                      </td>
                      <td className="px-3 py-1 text-right space-x-2">
                        <button
                          className="text-xs underline"
                          onClick={() => startEdit(b)}
                        >
                          edit
                        </button>
                        <button
                          className="text-xs underline text-red-300"
                          onClick={() => remove(b.id)}
                          disabled={loading}
                        >
                          delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
