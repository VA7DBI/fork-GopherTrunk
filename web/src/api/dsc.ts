// DSC / marine-distress-channel client. Mirrors GET /api/v1/dsc/messages.

import { type ClientConfig, joinURL } from "./client";

export interface DSCMessage {
  id: number;
  received_at: string;
  format: string;
  category: string;
  self_mmsi: number;
  target_mmsi?: number;
  nature?: string;
  time_utc?: string;

  // Position fields — present on distress alerts with a non-
  // sentinel position field.
  latitude?: number;
  longitude?: number;
  has_position: boolean;

  body?: string;
  raw_hex?: string;
}

export async function fetchDSCMessages(
  cfg: ClientConfig,
  limit = 200,
): Promise<DSCMessage[]> {
  const url = joinURL(
    cfg.baseURL,
    `/api/v1/dsc/messages?limit=${encodeURIComponent(String(limit))}`,
  );
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`dsc/messages ${res.status}`);
  return (await res.json()) as DSCMessage[];
}
