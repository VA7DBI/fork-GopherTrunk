// MDC1200 signaling client. Mirrors GET /api/v1/mdc1200/messages.

import { type ClientConfig, joinURL } from "./client";

export interface MDC1200Message {
  id: number;
  received_at: string;
  op: number;
  arg: number;
  unit_id: number;
  operation?: string;
  body?: string;
  raw_hex?: string;
  crc_ok: boolean;
}

export async function fetchMDC1200Messages(
  cfg: ClientConfig,
  limit = 200,
): Promise<MDC1200Message[]> {
  const url = joinURL(
    cfg.baseURL,
    `/api/v1/mdc1200/messages?limit=${encodeURIComponent(String(limit))}`,
  );
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`mdc1200/messages ${res.status}`);
  return (await res.json()) as MDC1200Message[];
}
