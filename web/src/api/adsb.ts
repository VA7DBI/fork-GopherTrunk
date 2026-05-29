// ADS-B / aircraft client. Mirrors GET /api/v1/adsb/aircraft.

import { type ClientConfig, joinURL } from "./client";

export interface AircraftReport {
  id: number;
  received_at: string;
  icao: number;
  icao_hex: string;
  kind: string;
  body?: string;
  crc_valid: boolean;

  // Identification fields (kind = "ident").
  callsign?: string;
  category?: number;

  // Position fields (kind = "airborne-pos" / "surface-pos").
  latitude?: number;
  longitude?: number;
  altitude_ft?: number;
  has_position: boolean;
  has_altitude: boolean;

  // Velocity fields (kind = "velocity").
  ground_speed_kn?: number;
  track_deg?: number;
  vertical_rate_fpm?: number;

  raw_hex?: string;
}

export async function fetchAircraftReports(
  cfg: ClientConfig,
  limit = 200,
): Promise<AircraftReport[]> {
  const url = joinURL(
    cfg.baseURL,
    `/api/v1/adsb/aircraft?limit=${encodeURIComponent(String(limit))}`,
  );
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`adsb/aircraft ${res.status}`);
  return (await res.json()) as AircraftReport[];
}
