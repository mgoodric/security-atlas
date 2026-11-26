package hostcapacity

import (
	"reflect"
	"testing"
)

// TestStructuralOverCollectionGuard pins Host's field set: three readings,
// the CPU sample count, the host name and the window. A field that could
// carry the raw points, other tags, or anything else from the response has
// to be added here on purpose.
func TestStructuralOverCollectionGuard(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"HostName": true, "MemoryUsedPercent": true, "CPUP95Percent": true,
		"CPUSampleCount": true, "DiskUsedPercent": true,
		"WindowStart": true, "WindowEnd": true,
	}
	typ := reflect.TypeOf(Host{})
	if typ.NumField() != len(allowed) {
		t.Errorf("Host has %d fields, want %d", typ.NumField(), len(allowed))
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !allowed[f.Name] {
			t.Errorf("Host has un-allow-listed field %q", f.Name)
		}
		switch f.Type.Kind() {
		case reflect.Slice, reflect.Map, reflect.Interface:
			t.Errorf("Host.%s is a %s; Host holds scalars only", f.Name, f.Type.Kind())
		}
	}
}
