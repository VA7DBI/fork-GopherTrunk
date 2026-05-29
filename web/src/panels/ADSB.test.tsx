import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/adsb", () => ({
  fetchAircraftReports: vi.fn(),
}));

import { fetchAircraftReports } from "../api/adsb";
import { useShared } from "../store/shared";
import { ADSB } from "./ADSB";

function resetStore() {
  useShared.setState({
    serverURL: "http://localhost:8080",
    token: null,
    connected: true,
    wsStatus: "idle",
    mutations: null,
    lastError: null,
    events: [],
    activeCalls: [],
    devices: [],
    systems: [],
    talkgroups: [],
    health: null,
    audio: null,
    scanner: null,
  });
}

describe("ADS-B panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
  });

  it("renders an empty-state when no messages are present", async () => {
    vi.mocked(fetchAircraftReports).mockResolvedValue([]);
    render(<ADSB />);
    await waitFor(() => {
      expect(screen.getByText(/No ADS-B messages yet/)).toBeInTheDocument();
    });
  });

  it("renders position rows with ICAO + lat/lon + altitude", async () => {
    vi.mocked(fetchAircraftReports).mockResolvedValue([
      {
        id: 1,
        received_at: "2026-05-26T12:34:56Z",
        icao: 0x40621d,
        icao_hex: "40621D",
        kind: "airborne-pos",
        body: "AIRBORNE-POS 40621D",
        crc_valid: true,
        latitude: 52.2572,
        longitude: 3.91937,
        altitude_ft: 38000,
        has_position: true,
        has_altitude: true,
      },
    ]);
    render(<ADSB />);
    await waitFor(() => {
      expect(screen.getByText("40621D")).toBeInTheDocument();
      expect(screen.getByText(/52\.2572, 3\.9194/)).toBeInTheDocument();
      expect(screen.getByText("38,000")).toBeInTheDocument();
    });
  });

  it("renders identification rows with callsign", async () => {
    vi.mocked(fetchAircraftReports).mockResolvedValue([
      {
        id: 2,
        received_at: "2026-05-26T12:34:56Z",
        icao: 0x4840d6,
        icao_hex: "4840D6",
        kind: "ident",
        callsign: "KLM1023",
        category: 4,
        crc_valid: true,
        has_position: false,
        has_altitude: false,
      },
    ]);
    render(<ADSB />);
    await waitFor(() => {
      expect(screen.getByText("4840D6")).toBeInTheDocument();
      expect(screen.getByText("KLM1023")).toBeInTheDocument();
    });
  });

  it("renders velocity rows with speed + track + VR", async () => {
    vi.mocked(fetchAircraftReports).mockResolvedValue([
      {
        id: 3,
        received_at: "2026-05-26T12:34:56Z",
        icao: 0x485020,
        icao_hex: "485020",
        kind: "velocity",
        ground_speed_kn: 159,
        track_deg: 182.88,
        vertical_rate_fpm: -832,
        crc_valid: true,
        has_position: false,
        has_altitude: false,
      },
    ]);
    render(<ADSB />);
    await waitFor(() => {
      expect(screen.getByText("485020")).toBeInTheDocument();
      expect(screen.getByText(/159kn \/ 183°/)).toBeInTheDocument();
      expect(screen.getByText("-832")).toBeInTheDocument();
    });
  });

  it("surfaces fetch errors", async () => {
    vi.mocked(fetchAircraftReports).mockRejectedValue(new Error("daemon down"));
    render(<ADSB />);
    await waitFor(() => {
      expect(screen.getByText(/daemon down/)).toBeInTheDocument();
    });
  });
});
