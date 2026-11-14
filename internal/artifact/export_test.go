package artifact

// PresignerOf exposes the presigner Build installed, so the black-box
// tests can check the URLs it signs without a database row.
func PresignerOf(s *Store) Presigner { return s.presigner }
