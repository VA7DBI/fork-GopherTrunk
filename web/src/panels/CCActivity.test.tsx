import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

import { useShared } from "../store/shared";
import type { EventDTO } from "../api/types";
import { CCActivity } from "./CCActivity";

// CCActivity embeds react-router <Link> for clickable RIDs, so every
// render() needs a Router context.
function renderPanel() {
  return render(
    <MemoryRouter>
      <CCActivity />
    </MemoryRouter>,
  );
}

function setEvents(events: EventDTO[]) {
  useShared.setState({
    serverURL: "http://localhost:8080",
    token: null,
    connected: true,
    wsStatus: "idle",
    mutations: null,
    lastError: null,
    events,
    activeCalls: [],
    devices: [],
    systems: [],
    talkgroups: [],
    health: null,
    audio: null,
    scanner: null,
  });
}

describe("CCActivity panel", () => {
  beforeEach(() => {
    setEvents([]);
  });

  it("renders the empty state when no CC events are in the store", () => {
    renderPanel();
    expect(screen.getByText(/Nothing here yet/)).toBeInTheDocument();
  });

  it("ignores non-CC events (silence on unrelated kinds)", () => {
    setEvents([
      {
        kind: "tone.alert",
        timestamp: "2026-05-26T12:00:00Z",
        payload: { profile: "knox" },
      },
      {
        kind: "sdr.attached",
        timestamp: "2026-05-26T12:00:00Z",
        payload: { serial: "rtl-1" },
      },
    ]);
    renderPanel();
    expect(screen.getByText(/Nothing here yet/)).toBeInTheDocument();
  });

  it("renders grant events with talkgroup and frequency", () => {
    setEvents([
      {
        kind: "grant",
        timestamp: "2026-05-26T12:34:56Z",
        payload: {
          system: "Metro P25",
          protocol: "p25",
          group_id: 1234,
          source_id: 5678,
          frequency_hz: 851_012_500,
          emergency: true,
        },
      },
    ]);
    renderPanel();
    // "Grant" appears in both the kind-filter <option> and the row's
    // Kind cell; assert via the row containing the system label.
    const row = screen.getByText("Metro P25").closest("tr");
    expect(row).not.toBeNull();
    expect(row!.textContent).toMatch(/Grant/);
    expect(row!.textContent).toMatch(/TG 1234/);
    expect(row!.textContent).toMatch(/851\.0125 MHz/);
    expect(row!.textContent).toMatch(/EMERG/);
  });

  it("renders affiliation events with response code", () => {
    setEvents([
      {
        kind: "affiliation",
        timestamp: "2026-05-26T12:00:00Z",
        payload: {
          system: "Metro P25",
          radio_id: 999,
          group_id: 100,
          response_code: 2,
        },
      },
    ]);
    renderPanel();
    const row = screen.getByText("Metro P25").closest("tr");
    expect(row).not.toBeNull();
    expect(row!.textContent).toMatch(/Affiliation/);
    expect(row!.textContent).toMatch(/radio 999/);
    expect(row!.textContent).toMatch(/TG 100/);
    expect(row!.textContent).toMatch(/resp 2/);
  });

  it("supports filtering by kind", async () => {
    setEvents([
      {
        kind: "grant",
        timestamp: "2026-05-26T12:00:00Z",
        payload: { system: "Sys-A", group_id: 1 },
      },
      {
        kind: "affiliation",
        timestamp: "2026-05-26T12:01:00Z",
        payload: { system: "Sys-A", radio_id: 100, group_id: 1 },
      },
    ]);
    renderPanel();
    // Two rows initially.
    expect(screen.getByText("2 matching events")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByRole("combobox"), "grant");
    expect(screen.getByText("1 matching event")).toBeInTheDocument();
  });

  it("supports filtering by system substring", async () => {
    setEvents([
      {
        kind: "grant",
        timestamp: "2026-05-26T12:00:00Z",
        payload: { system: "Metro P25", group_id: 1 },
      },
      {
        kind: "grant",
        timestamp: "2026-05-26T12:01:00Z",
        payload: { system: "County DMR", group_id: 2 },
      },
    ]);
    renderPanel();
    expect(screen.getByText("Metro P25")).toBeInTheDocument();
    expect(screen.getByText("County DMR")).toBeInTheDocument();

    await userEvent.type(screen.getByPlaceholderText(/filter system/), "metro");
    expect(screen.getByText("Metro P25")).toBeInTheDocument();
    expect(screen.queryByText("County DMR")).not.toBeInTheDocument();
  });

  it("renders patch events with member count", () => {
    setEvents([
      {
        kind: "patch",
        timestamp: "2026-05-26T12:00:00Z",
        payload: {
          system: "Metro P25",
          super_group: 999,
          members: [101, 102, 103],
          add: true,
        },
      },
    ]);
    renderPanel();
    const row = screen.getByText("Metro P25").closest("tr");
    expect(row).not.toBeNull();
    expect(row!.textContent).toMatch(/Patch/);
    expect(row!.textContent).toMatch(/super-group 999/);
    expect(row!.textContent).toMatch(/3 members/);
    expect(row!.textContent).toMatch(/· add/);
  });

  it("renders patch cancel events when add is false", () => {
    setEvents([
      {
        kind: "patch",
        timestamp: "2026-05-26T12:00:00Z",
        payload: {
          system: "Metro P25",
          super_group: 999,
          members: [101],
          add: false,
        },
      },
    ]);
    renderPanel();
    const row = screen.getByText("Metro P25").closest("tr");
    expect(row).not.toBeNull();
    expect(row!.textContent).toMatch(/super-group 999/);
    expect(row!.textContent).toMatch(/· cancel/);
  });

  it("renders talker alias events", () => {
    setEvents([
      {
        kind: "talker.alias",
        timestamp: "2026-05-26T12:00:00Z",
        payload: {
          system: "Metro P25",
          source: 1234,
          alias: "ENG-5",
        },
      },
    ]);
    renderPanel();
    const row = screen.getByText("Metro P25").closest("tr");
    expect(row).not.toBeNull();
    expect(row!.textContent).toMatch(/Talker alias/);
    expect(row!.textContent).toMatch(/radio 1234: "ENG-5"/);
  });
});
