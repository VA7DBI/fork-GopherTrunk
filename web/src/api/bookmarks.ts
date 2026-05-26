// Bookmarks CRUD client. Mirrors the REST routes registered in
// internal/api/handlers_bookmarks.go.

import { type ClientConfig, joinURL } from "./client";

export interface Bookmark {
  id: number;
  name: string;
  freq_hz: number;
  mode: string;
  ctcss_hz?: number;
  dcs_code?: number;
  notes?: string;
  group?: string;
  created_at: string;
  updated_at: string;
}

export type BookmarkInput = Omit<Bookmark, "id" | "created_at" | "updated_at">;

async function jsonRequest<T>(
  cfg: ClientConfig,
  method: string,
  path: string,
  body?: unknown,
): Promise<T | null> {
  const url = joinURL(cfg.baseURL, path);
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(url, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return null;
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${path} ${res.status}: ${text || res.statusText}`);
  }
  return (await res.json()) as T;
}

export const bookmarks = {
  list: (cfg: ClientConfig) =>
    jsonRequest<Bookmark[]>(cfg, "GET", "/api/v1/bookmarks").then(
      (v) => v ?? [],
    ),
  create: (cfg: ClientConfig, b: BookmarkInput) =>
    jsonRequest<Bookmark>(cfg, "POST", "/api/v1/bookmarks", b),
  update: (cfg: ClientConfig, id: number, b: BookmarkInput) =>
    jsonRequest<Bookmark>(cfg, "PATCH", `/api/v1/bookmarks/${id}`, b),
  remove: (cfg: ClientConfig, id: number) =>
    jsonRequest<null>(cfg, "DELETE", `/api/v1/bookmarks/${id}`),
};
