// Package crosswalks embeds the bundled framework crosswalk YAML files so the
// binary can read their requirement titles and bodies without the repo on
// disk. The workbook importer (internal/api/soc2import) titles
// workbook-derived requirements from the bundled file for the same framework
// slug (#1730). Importing a bundled crosswalk itself still reads the file by
// path: atlas-cli catalog import-crosswalk data/crosswalks/<file>.yaml.
package crosswalks

import (
	"embed"
	"io/fs"
)

//go:embed *.yaml
var embedded embed.FS

// EmbeddedFS returns the bundled *.yaml crosswalks as an fs.FS.
func EmbeddedFS() fs.FS { return embedded }
