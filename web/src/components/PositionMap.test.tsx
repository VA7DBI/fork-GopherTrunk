import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

// Leaflet uses DOM APIs jsdom doesn't fully implement (canvas
// rendering, getBoundingClientRect, etc.). Stub it with a minimal
// mock that records the operations PositionMap performs so the
// test asserts behaviour without standing up a real map.
const mapInstance = {
  remove: vi.fn(),
  fitBounds: vi.fn(),
};

interface MarkerStub {
  latlng: [number, number];
  style: { fillColor?: string };
  tooltipHTML?: string;
  setLatLng: ReturnType<typeof vi.fn>;
  setStyle: ReturnType<typeof vi.fn>;
  bindTooltip: ReturnType<typeof vi.fn>;
  addTo: ReturnType<typeof vi.fn>;
  remove: ReturnType<typeof vi.fn>;
}

const createdMarkers: MarkerStub[] = [];

vi.mock("leaflet", () => {
  const L = {
    map: vi.fn(() => mapInstance),
    tileLayer: vi.fn(() => ({
      addTo: vi.fn(() => ({})),
    })),
    circleMarker: vi.fn((latlng: [number, number], opts: { fillColor: string }) => {
      const m: MarkerStub = {
        latlng,
        style: { fillColor: opts.fillColor },
        setLatLng: vi.fn(function (this: MarkerStub, ll: [number, number]) {
          this.latlng = ll;
          return this;
        }),
        setStyle: vi.fn(function (this: MarkerStub, s: { fillColor?: string }) {
          if (s.fillColor) this.style.fillColor = s.fillColor;
          return this;
        }),
        bindTooltip: vi.fn(function (this: MarkerStub, html: string) {
          this.tooltipHTML = html;
          return this;
        }),
        addTo: vi.fn(function (this: MarkerStub) {
          return this;
        }),
        remove: vi.fn(),
      };
      createdMarkers.push(m);
      return m;
    }),
    latLngBounds: vi.fn((coords: [number, number][]) => ({ coords })),
  };
  return { default: L };
});

// Leaflet ships a CSS file with the package; the dynamic-import
// side-effect would 404 in jsdom. Stub it.
vi.mock("leaflet/dist/leaflet.css", () => ({}));

import { PositionMap, type MapPoint } from "./PositionMap";

describe("PositionMap", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    createdMarkers.length = 0;
  });

  it("renders a map container with a stable test ID", () => {
    render(<PositionMap points={[]} />);
    expect(screen.getByTestId("position-map")).toBeInTheDocument();
  });

  it("creates one CircleMarker per point with the kind's color", () => {
    const points: MapPoint[] = [
      {
        id: "aprs-1",
        latitude: 49.0,
        longitude: -72.0,
        kind: "aprs",
        label: "W1AW-9",
      },
      {
        id: "adsb-1",
        latitude: 52.25,
        longitude: 3.91,
        kind: "adsb",
        label: "KLM1023",
        detail: "38,000 ft",
      },
    ];
    render(<PositionMap points={points} />);
    expect(createdMarkers).toHaveLength(2);
    // APRS = blue, ADS-B = purple per the KIND_COLOR table.
    expect(createdMarkers[0].style.fillColor).toBe("#3b82f6");
    expect(createdMarkers[1].style.fillColor).toBe("#a855f7");
  });

  it("escapes HTML in tooltip labels to prevent XSS", () => {
    const points: MapPoint[] = [
      {
        id: "evil",
        latitude: 0,
        longitude: 0,
        kind: "default",
        label: "<script>alert('x')</script>",
      },
    ];
    render(<PositionMap points={points} />);
    expect(createdMarkers).toHaveLength(1);
    expect(createdMarkers[0].tooltipHTML).toContain("&lt;script&gt;");
    expect(createdMarkers[0].tooltipHTML).not.toContain("<script>");
  });

  it("highlights distress markers with the red distress color + larger radius", () => {
    const points: MapPoint[] = [
      {
        id: "dsc-1",
        latitude: 37.8,
        longitude: -122.4,
        kind: "dsc-distress",
        label: "MMSI 366053209",
        detail: "fire / explosion",
      },
    ];
    render(<PositionMap points={points} />);
    expect(createdMarkers[0].style.fillColor).toBe("#ef4444");
  });

  it("auto-fits the camera when points are present", () => {
    const points: MapPoint[] = [
      { id: "a", latitude: 49.0, longitude: -72.0, kind: "aprs", label: "" },
      { id: "b", latitude: 50.0, longitude: -70.0, kind: "aprs", label: "" },
    ];
    render(<PositionMap points={points} />);
    expect(mapInstance.fitBounds).toHaveBeenCalled();
  });
});
