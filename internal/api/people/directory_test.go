// Pure-Go unit tests for Directory.Name (#2286). No Postgres.

package people

import "testing"

func TestDirectoryName(t *testing.T) {
	t.Parallel()
	const (
		alice    = "6f1c2a3e-1111-4a5b-9c8d-0123456789ab"
		gone     = "6f1c2a3e-2222-4a5b-9c8d-0123456789ab"
		nameless = "6f1c2a3e-3333-4a5b-9c8d-0123456789ab"
		other    = "6f1c2a3e-4444-4a5b-9c8d-0123456789ab"
		key      = "0b9e8d7c-5555-4a5b-9c8d-00000000beef"
		otherKey = "0b9e8d7c-6666-4a5b-9c8d-00000000cafe"
	)
	d := NewDirectory(Response{
		Users: []User{
			{ID: alice, DisplayName: "Alice Admin", Status: "active"},
			{ID: gone, DisplayName: "Gone Person", Status: "disabled"},
			{ID: nameless, DisplayName: "  ", Status: "active"},
		},
		APIKeys: []APIKey{{ID: key, OwnerID: alice}},
	})
	cases := []struct{ actor, want string }{
		{alice, "Alice Admin"},
		{"user:" + alice, "Alice Admin"},
		{gone, "Former user"},
		{nameless, "Unknown"},
		{"key_" + key, "API key …beef"},
		{"system", "System"},
		// A platform process, such as the SCF import's carry-forward (#2554).
		{"system:scf-import", "System"},
		{"systemd", "Unknown"},
		// Another tenant's user or key is not in this directory.
		{other, "Unknown"},
		{"key_" + otherKey, "Unknown"},
		// A per-token session id names no person.
		{"jwt:3f2a9c", "Unknown"},
		// A non-canonical uuid names no user (identity.UserID, #2478).
		{"{" + alice + "}", "Unknown"},
		{"key_" + "0B9E8D7C-5555-4A5B-9C8D-00000000BEEF", "Unknown"},
		{"bootstrap-admin", "Unknown"},
		{"", "Unknown"},
	}
	for _, c := range cases {
		if got := d.Name(c.actor); got != c.want {
			t.Errorf("Name(%q) = %q, want %q", c.actor, got, c.want)
		}
	}
	var none *Directory
	if got := none.Name(alice); got != "Unknown" {
		t.Errorf("nil Directory Name = %q, want Unknown", got)
	}
	if got := none.Name("system"); got != "System" {
		t.Errorf("nil Directory Name(system) = %q, want System", got)
	}
}
