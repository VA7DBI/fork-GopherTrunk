import { useMemo, useState } from "react";

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => React.ReactNode;
  sort?: (a: T, b: T) => number;
  className?: string;
  headerClassName?: string;
}

interface Props<T> {
  rows: T[];
  columns: Column<T>[];
  rowKey: (row: T, index: number) => string;
  defaultSortKey?: string;
  defaultSortDirection?: "asc" | "desc";
  onRowClick?: (row: T, key: string) => void;
  emptyMessage?: string;
  // Optional second-row "expanded" content rendered beneath the row
  // (used by Events to show payload JSON inline).
  renderExpansion?: (row: T) => React.ReactNode;
  expandedKey?: string | null;
}

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  defaultSortKey,
  defaultSortDirection = "asc",
  onRowClick,
  emptyMessage = "No data.",
  renderExpansion,
  expandedKey,
}: Props<T>) {
  const [sortKey, setSortKey] = useState<string | null>(
    defaultSortKey ?? null,
  );
  const [sortDir, setSortDir] = useState<"asc" | "desc">(
    defaultSortDirection,
  );

  const sorted = useMemo(() => {
    if (!sortKey) return rows;
    const col = columns.find((c) => c.key === sortKey);
    if (!col?.sort) return rows;
    const copy = rows.slice();
    copy.sort(col.sort);
    if (sortDir === "desc") copy.reverse();
    return copy;
  }, [rows, columns, sortKey, sortDir]);

  function toggleSort(key: string) {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  }

  if (rows.length === 0) {
    return <p className="text-muted text-sm">{emptyMessage}</p>;
  }

  return (
    <div className="panel overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-panel/80 text-xs uppercase tracking-wider text-muted">
            <tr>
              {columns.map((c) => {
                const sortable = !!c.sort;
                const active = sortKey === c.key;
                return (
                  <th
                    key={c.key}
                    scope="col"
                    className={`px-3 py-2 text-left font-medium ${
                      c.headerClassName ?? ""
                    } ${sortable ? "cursor-pointer select-none" : ""}`}
                    onClick={sortable ? () => toggleSort(c.key) : undefined}
                    aria-sort={
                      !sortable
                        ? undefined
                        : active
                          ? sortDir === "asc"
                            ? "ascending"
                            : "descending"
                          : "none"
                    }
                  >
                    <span className="inline-flex items-center gap-1">
                      {c.header}
                      {sortable && (
                        <span
                          aria-hidden
                          className={active ? "text-accent" : "opacity-40"}
                        >
                          {active ? (sortDir === "asc" ? "▲" : "▼") : "⇅"}
                        </span>
                      )}
                    </span>
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody className="divide-y divide-panel">
            {sorted.map((row, i) => {
              const key = rowKey(row, i);
              const expanded = expandedKey === key;
              return (
                <Row
                  key={key}
                  row={row}
                  columns={columns}
                  onClick={
                    onRowClick ? () => onRowClick(row, key) : undefined
                  }
                  expansion={
                    renderExpansion && expanded ? renderExpansion(row) : null
                  }
                />
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Row<T>({
  row,
  columns,
  onClick,
  expansion,
}: {
  row: T;
  columns: Column<T>[];
  onClick?: () => void;
  expansion: React.ReactNode | null;
}) {
  return (
    <>
      <tr
        className={
          onClick
            ? "hover:bg-panel/40 cursor-pointer focus-within:bg-panel/40"
            : ""
        }
        onClick={onClick}
        tabIndex={onClick ? 0 : -1}
        onKeyDown={
          onClick
            ? (e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onClick();
                }
              }
            : undefined
        }
      >
        {columns.map((c) => (
          <td key={c.key} className={`px-3 py-2 align-top ${c.className ?? ""}`}>
            {c.render(row)}
          </td>
        ))}
      </tr>
      {expansion && (
        <tr>
          <td
            colSpan={columns.length}
            className="px-3 pb-3 pt-0 bg-panel/30 text-xs"
          >
            {expansion}
          </td>
        </tr>
      )}
    </>
  );
}
