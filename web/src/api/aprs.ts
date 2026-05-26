// APRS packet log client. Mirrors GET /api/v1/aprs/packets.

import { type ClientConfig, joinURL } from "./client";

export interface APRSPacket {
  id: number;
  received_at: string;
  src: string;
  dst: string;
  path?: string;
  type: string;
  body?: string;
  latitude?: number;
  longitude?: number;
  raw_info?: string;
  fcs_ok: boolean;
}

export async function fetchAPRSPackets(
  cfg: ClientConfig,
  limit = 200,
): Promise<APRSPacket[]> {
  const url = joinURL(
    cfg.baseURL,
    `/api/v1/aprs/packets?limit=${encodeURIComponent(String(limit))}`,
  );
  const headers: Record<string, string> = { Accept: "application/json" };
  if (cfg.token) headers["Authorization"] = `Bearer ${cfg.token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`aprs/packets ${res.status}`);
  return (await res.json()) as APRSPacket[];
}
