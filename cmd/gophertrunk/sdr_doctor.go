package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/config"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/plutoplus"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/rtlsdr/purego"
	"github.com/MattCheramie/GopherTrunk/internal/sdr/rtlsdr/usb"
)

// runSDRDoctor implements `gophertrunk sdr doctor`. It iterates the
// librtlsdr VID/PID whitelist, asks the platform inspector which
// kernel/Windows function driver is bound to each matching dongle,
// and prints a row per device with an actionable next step. Read-only:
// never opens or claims a USB device, so safe to run as a regular
// user alongside a live daemon.
func runSDRDoctor(args []string) {
	fs := flag.NewFlagSet("sdr doctor", flag.ExitOnError)
	verbose := fs.Bool("v", false, "include extra columns (driver description)")
	configPath := fs.String("config", "", "optional config file path (used to inspect sdr.pluto_plus entries)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	inspector := usb.DefaultDriverInspector()
	var rows []usb.DriverBinding
	var failures []error
	seen := make(map[string]bool)
	for _, pair := range purego.KnownVIDPIDs() {
		bindings, err := inspector.Inspect(pair.VID, pair.PID)
		if err != nil {
			if errors.Is(err, usb.ErrUnsupportedPlatform) {
				fmt.Fprintf(os.Stderr, "sdr doctor: no driver inspector for %s/%s\n", runtime.GOOS, runtime.GOARCH)
				return
			}
			failures = append(failures, fmt.Errorf("inspect %04x:%04x: %w", pair.VID, pair.PID, err))
			continue
		}
		for _, b := range bindings {
			key := fmt.Sprintf("%04x:%04x:%s", b.Descriptor.VID, b.Descriptor.PID, b.Descriptor.Path)
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, b)
		}
	}

	for _, err := range failures {
		fmt.Fprintln(os.Stderr, "sdr doctor:", err)
	}

	if len(rows) == 0 {
		fmt.Println("No RTL-SDR dongles found.")
		fmt.Println("If a dongle is plugged in but missing here:")
		switch runtime.GOOS {
		case "windows":
			fmt.Println("  - Open Device Manager and confirm the dongle appears (look for an exclamation mark).")
			fmt.Println("  - Re-plug into a different USB port (preferably USB 2.0 for first-time bring-up).")
			fmt.Println("  - Run Zadig from the Start Menu and verify the dongle is listed with Options → List All Devices.")
		case "linux":
			fmt.Println("  - Run `lsusb` and confirm the dongle's VID:PID appears (RTL-SDR is typically 0bda:2832 or 0bda:2838).")
			fmt.Println("  - Confirm /sys/bus/usb/devices exists and your user has read permission.")
		}
	} else {
		printDoctorRows(rows, *verbose)
	}

	printPlutoDoctor(*configPath)
}

// printDoctorRows writes a tab-aligned per-dongle status table to
// stdout. Mirrors listSDRs's column widths so operators see a
// consistent layout across `sdr list` and `sdr doctor`.
func printDoctorRows(rows []usb.DriverBinding, verbose bool) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(tw, "VID:PID\tSERIAL\tDRIVER\tEXPECTED\tSTATUS\tDESCRIPTION\tNEXT-STEP")
	} else {
		fmt.Fprintln(tw, "VID:PID\tSERIAL\tDRIVER\tEXPECTED\tSTATUS\tNEXT-STEP")
	}
	for _, r := range rows {
		status := "BAD"
		if r.OK {
			status = "OK"
		}
		driver := r.DriverName
		if driver == "" {
			driver = "(none)"
		}
		hint := r.Hint
		if hint == "" {
			hint = "-"
		}
		vidpid := fmt.Sprintf("%04x:%04x", r.Descriptor.VID, r.Descriptor.PID)
		serial := r.Descriptor.Serial
		if serial == "" {
			serial = "-"
		}
		if verbose {
			descr := r.DriverDesc
			if descr == "" {
				descr = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				vidpid, serial, driver, r.Expected, status, descr, hint)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				vidpid, serial, driver, r.Expected, status, hint)
		}
	}
	tw.Flush()
}

type plutoDoctorRow struct {
	Transport string
	Endpoint  string
	Serial    string
	Status    string
	Stage     string
	Hint      string
}

func printPlutoDoctor(configPath string) {
	specs := doctorPlutoSpecs(configPath)
	if len(specs) == 0 {
		return
	}

	rows := make([]plutoDoctorRow, 0, len(specs))
	for i, spec := range specs {
		d := plutoplus.New([]plutoplus.Spec{spec}, nil)
		dev, err := d.Open(0)
		transport := spec.Transport
		if strings.TrimSpace(transport) == "" {
			transport = plutoplus.TransportTCP
		}
		endpoint := spec.Addr
		if transport == plutoplus.TransportUSB && strings.TrimSpace(endpoint) == "" {
			endpoint = plutoplus.DefaultUSBAddr
		}
		serial := spec.Serial
		if strings.TrimSpace(serial) == "" {
			serial = fmt.Sprintf("plutoplus[%d]", i)
		}
		if err == nil {
			_ = dev.Close()
			rows = append(rows, plutoDoctorRow{
				Transport: transport,
				Endpoint:  endpoint,
				Serial:    serial,
				Status:    "OK",
				Stage:     "-",
				Hint:      "endpoint reachable and handshake succeeded",
			})
			continue
		}

		stage := "unknown"
		hint := "inspect endpoint logs and transport settings"
		if s, ok := plutoplus.ErrorStage(err); ok {
			stage = string(s)
			switch s {
			case plutoplus.StageDial:
				hint = "endpoint unreachable; check host/port, USB network interface, and firewall"
			case plutoplus.StageHandshake:
				hint = "wrong service or protocol; expected rtl_tcp-compatible header"
			case plutoplus.StageCommand:
				hint = "command channel write failed; inspect endpoint stability"
			case plutoplus.StageStream:
				hint = "stream read failed; check link quality and endpoint runtime"
			}
		}
		rows = append(rows, plutoDoctorRow{
			Transport: transport,
			Endpoint:  endpoint,
			Serial:    serial,
			Status:    "BAD",
			Stage:     stage,
			Hint:      hint,
		})
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "PLUTO TRANSPORT\tENDPOINT\tSERIAL\tSTATUS\tSTAGE\tNEXT-STEP")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Transport, r.Endpoint, r.Serial, r.Status, r.Stage, r.Hint)
	}
	tw.Flush()
}

func doctorPlutoSpecs(configPath string) []plutoplus.Spec {
	if strings.TrimSpace(configPath) == "" {
		return []plutoplus.Spec{{Transport: plutoplus.TransportUSB}}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdr doctor: could not load config %q: %v\n", configPath, err)
		return nil
	}
	if len(cfg.SDR.PlutoPlus) == 0 {
		return []plutoplus.Spec{{Transport: plutoplus.TransportUSB}}
	}
	out := make([]plutoplus.Spec, 0, len(cfg.SDR.PlutoPlus))
	for _, p := range cfg.SDR.PlutoPlus {
		out = append(out, plutoplus.Spec{
			Addr:           p.Addr,
			Serial:         p.Serial,
			Role:           p.Role,
			Transport:      p.Transport,
			ConnectTimeout: time.Duration(p.ConnectTimeoutMs) * time.Millisecond,
		})
	}
	return out
}
