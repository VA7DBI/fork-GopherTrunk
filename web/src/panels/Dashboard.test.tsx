import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("../api/client", () => ({
	api: {
		health: vi.fn(),
		activeCalls: vi.fn(),
		devices: vi.fn(),
		audio: vi.fn(),
		runtime: vi.fn(),
	},
}));

import { api } from "../api/client";
import { useShared } from "../store/shared";
import { Dashboard } from "./Dashboard";

describe("Dashboard Pluto health", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		useShared.setState({
			serverURL: "http://localhost:8080",
			token: null,
			connected: true,
			wsStatus: "open",
			mutations: null,
			lastError: null,
			events: [],
			activeCalls: [],
			devices: [],
			health: null,
			audio: null,
			scanner: null,
			systems: [],
			talkgroups: [],
			rids: [],
		});
		vi.mocked(api.health).mockResolvedValue({ status: "ok", active_calls: 0, pool_attached_count: 0 });
		vi.mocked(api.activeCalls).mockResolvedValue([]);
		vi.mocked(api.devices).mockResolvedValue([]);
		vi.mocked(api.audio).mockResolvedValue({ backend_enabled: false, sample_rate: 0, muted: false, recording_enabled: false });
	});

	it("renders Pluto health summary when runtime exposes plutoplus counters", async () => {
		vi.mocked(api.runtime).mockResolvedValue({
			sdr_backends: ["plutoplus"],
			pluto_runtime: {
				reconnects: 5,
				dial_failures: 2,
				handshake_failures: 1,
				stream_failures: 4,
			},
		});

		render(<Dashboard />);

		expect(await screen.findByText("Pluto Plus health")).toBeInTheDocument();
		expect(screen.getByText(/Reconnects/i)).toBeInTheDocument();
		expect(screen.getByText(/Failures/i)).toBeInTheDocument();
		expect(screen.getByText("unstable")).toBeInTheDocument();
		expect(screen.getByText(/dial 2/)).toBeInTheDocument();
		expect(screen.getByText(/handshake 1/)).toBeInTheDocument();
		expect(screen.getByText(/stream 4/)).toBeInTheDocument();
		expect(screen.getByText(/hint: check USB\/network stability and host performance under load/i)).toBeInTheDocument();
	});
});