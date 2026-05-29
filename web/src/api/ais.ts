// AIS / vessel-log client. Mirrors GET /api/v1/ais/vessels.

import { type ClientConfig, joinURL } from "./client";

export interface AISMessage {
  id: number;
  received_at: string;
  mmsi: number;
  type: string;
  body?: string;

  // Position fields — present on position-bearing types
  // (1/2/3/4/18/19/27).
  latitude?: number;
  longitude?: number;
  sog?: number; // speed over ground (knots)
  cog?: number; // course over ground (degrees)
  heading?: number;
  has_position: boolean;

  // Static-data fields — present on type-5 and type-24 messages.
  vessel_name?: string;
  callsign?: string;
  destination?: string;
  ship_type?: number;
  imo?: number;

  raw_hex?: string;
  fcs_ok: boolean;
}

export async function fetchAISVessels(
  cfg: ClientConfig,
  limit = 200,
): Promise<AISMessage[]> {
  const url = joinURL(
    cfg.baseURL,
    `/api/v1/ais/vessels?limit=${encodeURIComponent(String(limit))}`,
  );
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`ais/vessels ${res.status}`);
  return (await res.json()) as AISMessage[];
}
