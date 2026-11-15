package dashboardexport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// #2195: 3 controls started passing and 1 stopped, with a net change of +1
// (one control failed and recovered inside the window). The export states
// both lists and names delta as the net change, so "+1 / 1 flipped out" never
// reads alone.
func threeGainsOneLossSnapshot() Snapshot {
	s := fixtureSnapshot()
	s.Panels.Drift = DriftPanel{
		Since:           "2026-05-17",
		Through:         "2026-05-24",
		Delta:           1,
		FlippedOutCount: 1,
		FlippedOut: []DriftRow{
			{ControlID: "ctrl-out", LastPassing: "2026-05-20", CurrentResult: "fail"},
		},
		StartedPassingCount: 3,
		StartedPassing: []DriftGainRow{
			{ControlID: "ctrl-in-1"}, {ControlID: "ctrl-in-2"}, {ControlID: "ctrl-in-3"},
		},
	}
	return s
}

func TestDriftExport_JSON_ThreeGainsOneLoss(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := encodeJSON(&buf, threeGainsOneLossSnapshot()); err != nil {
		t.Fatalf("encodeJSON: %v", err)
	}
	var got struct {
		Panels struct {
			Drift struct {
				Delta               int `json:"delta"`
				FlippedOutCount     int `json:"flipped_out_count"`
				StartedPassingCount int `json:"started_passing_count"`
				StartedPassing      []struct {
					ControlID string `json:"control_id"`
				} `json:"started_passing"`
			} `json:"drift"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := got.Panels.Drift
	if d.Delta != 1 || d.FlippedOutCount != 1 || d.StartedPassingCount != 3 || len(d.StartedPassing) != 3 {
		t.Fatalf("drift panel = %+v; want delta 1, flipped_out 1, started_passing 3 with 3 rows", d)
	}
}

func TestDriftExport_Table_ThreeGainsOneLoss(t *testing.T) {
	t.Parallel()
	header, rows := driftTable(threeGainsOneLossSnapshot().Panels.Drift)
	if want := []string{"control_id", "last_passing", "current_result", "change"}; strings.Join(header, ",") != strings.Join(want, ",") {
		t.Fatalf("header = %v; want %v", header, want)
	}
	wantWindow := "__window__ since=2026-05-17 through=2026-05-24 net_change_in_passing=1 started_passing_count=3 flipped_out_count=1"
	if rows[0][0] != wantWindow {
		t.Errorf("window row = %q; want %q", rows[0][0], wantWindow)
	}
	changes := map[string]int{}
	for _, r := range rows[1:] {
		changes[r[3]]++
	}
	if changes["started_passing"] != 3 || changes["stopped_passing"] != 1 || len(rows) != 5 {
		t.Errorf("rows by change = %v (%d rows); want 3 started_passing + 1 stopped_passing", changes, len(rows))
	}
}
