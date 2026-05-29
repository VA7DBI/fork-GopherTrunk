import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/dsc", () => ({
  fetchDSCMessages: vi.fn(),
}));

import { fetchDSCMessages } from "../api/dsc";
import { useShared } from "../store/shared";
import { DSC } from "./DSC";

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

describe("DSC panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
  });

  it("renders an empty-state when no sequences are present", async () => {
    vi.mocked(fetchDSCMessages).mockResolvedValue([]);
    render(<DSC />);
    await waitFor(() => {
      expect(screen.getByText(/No DSC sequences yet/)).toBeInTheDocument();
    });
  });

  it("renders distress alerts with nature + position", async () => {
    vi.mocked(fetchDSCMessages).mockResolvedValue([
      {
        id: 1,
        received_at: "2026-05-26T12:34:56Z",
        format: "distress",
        category: "distress",
        self_mmsi: 366053209,
        nature: "fire / explosion",
        time_utc: "14:25",
        latitude: 37.8,
        longitude: 122.4,
        has_position: true,
        body: "DISTRESS MMSI=366053209 fire 37.80,122.40",
      },
    ]);
    render(<DSC />);
    await waitFor(() => {
      expect(screen.getByText("366053209")).toBeInTheDocument();
      expect(screen.getByText("fire / explosion")).toBeInTheDocument();
      expect(screen.getByText(/37\.8000, 122\.4000/)).toBeInTheDocument();
      expect(screen.getByText(/UTC 14:25/)).toBeInTheDocument();
    });
  });

  it("renders individual routine calls with target MMSI", async () => {
    vi.mocked(fetchDSCMessages).mockResolvedValue([
      {
        id: 2,
        received_at: "2026-05-26T12:34:56Z",
        format: "individual",
        category: "routine",
        self_mmsi: 366053209,
        target_mmsi: 3660000,
        has_position: false,
        body: "INDIVIDUAL routine",
      },
    ]);
    render(<DSC />);
    await waitFor(() => {
      expect(screen.getByText("366053209")).toBeInTheDocument();
      expect(screen.getByText("003660000")).toBeInTheDocument();
    });
  });

  it("surfaces fetch errors", async () => {
    vi.mocked(fetchDSCMessages).mockRejectedValue(new Error("daemon down"));
    render(<DSC />);
    await waitFor(() => {
      expect(screen.getByText(/daemon down/)).toBeInTheDocument();
    });
  });
});
