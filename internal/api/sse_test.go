package api

import (
	"encoding/json"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/MattCheramie/GopherTrunk/internal/trunking"
)

// TestEventToDTOAffiliationJSON pins the wire shape of the affiliation
// event so downstream consumers (Grafana, dashboards) don't break on a
// silent field-name change.
func TestEventToDTOAffiliationJSON(t *testing.T) {
	dto := eventToDTO(events.Event{
		Kind: events.KindAffiliation,
		Payload: trunking.Affiliation{
			System:            "MMR",
			Protocol:          "p25",
			SourceID:          0xABCDEF,
			GroupID:           0x1234,
			AnnouncementGroup: 0xAABB,
			Response:          trunking.AffiliationAccepted,
		},
	})
	body, err := json.Marshal(dto.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	want := `{"system":"MMR","protocol":"p25","source_id":11259375,"group_id":4660,"announcement_group":43707,"response":"accepted"}`
	if got != want {
		t.Errorf("affiliation JSON =\n  %s\nwant\n  %s", got, want)
	}
}

// TestEventToDTOUnitRegistrationJSON pins the wire shape of the
// registration event for the same reason.
func TestEventToDTOUnitRegistrationJSON(t *testing.T) {
	dto := eventToDTO(events.Event{
		Kind: events.KindUnitRegistration,
		Payload: trunking.UnitRegistration{
			System:   "MMR",
			Protocol: "p25",
			SourceID: 0x112233,
			WACN:     0xBEE08,
			SystemID: 0x534,
			Response: trunking.RegistrationAccepted,
		},
	})
	body, err := json.Marshal(dto.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	want := `{"system":"MMR","protocol":"p25","source_id":1122867,"wacn":781832,"system_id":1332,"response":"accepted"}`
	if got != want {
		t.Errorf("registration JSON =\n  %s\nwant\n  %s", got, want)
	}
}

// TestEventToDTOPatchJSON pins the wire shape of the patch event. The
// values mirror the report in issue #374 so this test doubles as the
// regression record for "CC Activity always shows super-group 0".
func TestEventToDTOPatchJSON(t *testing.T) {
	dto := eventToDTO(events.Event{
		Kind: events.KindPatch,
		Payload: trunking.Patch{
			System:     "MMR",
			Protocol:   "p25",
			SuperGroup: 32301,
			Members:    []uint32{32501},
			Vendor:     "motorola",
			Add:        true,
		},
	})
	body, err := json.Marshal(dto.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	want := `{"system":"MMR","protocol":"p25","super_group":32301,"members":[32501],"vendor":"motorola","add":true,"at":"0001-01-01T00:00:00Z"}`
	if got != want {
		t.Errorf("patch JSON =\n  %s\nwant\n  %s", got, want)
	}
}
