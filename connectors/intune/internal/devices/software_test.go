package devices

import (
	"context"
	"errors"
	"testing"
)

type fakeSoftwareAPI struct {
	out []RawDeviceSoftware
	err error
}

func (f *fakeSoftwareAPI) ListDetectedApps(_ context.Context) ([]RawDeviceSoftware, error) {
	return f.out, f.err
}

func TestCollectSoftware_MapsToSwinventoryShape(t *testing.T) {
	t.Parallel()
	api := &fakeSoftwareAPI{out: []RawDeviceSoftware{
		{DeviceID: "d-1", Apps: []RawSoftwareItem{{Name: "Chrome", Version: "125", AppID: "app-1"}}},
		{DeviceID: "  ", Apps: []RawSoftwareItem{{Name: "Dropped"}}},
	}}
	got, err := CollectSoftware(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectSoftware: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "d-1" {
		t.Fatalf("got %+v; want only d-1", got)
	}
	sw := got[0].Software
	if len(sw) != 1 || sw[0].Name != "Chrome" || sw[0].Version != "125" || sw[0].Identifier != "app-1" {
		t.Errorf("software map wrong: %+v", sw)
	}
}

func TestCollectSoftware_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := CollectSoftware(context.Background(), nil); err == nil {
		t.Fatal("want nil-API error")
	}
}

func TestCollectSoftware_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	_, err := CollectSoftware(context.Background(), &fakeSoftwareAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}
