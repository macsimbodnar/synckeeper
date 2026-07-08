// Package conflicts owns conflict-copy naming. A conflicted copy keeps the
// losing version under a name every machine can see:
// "<stem> (conflict <machine_name> <YYYY-MM-DD_HHMMSS>)<suffix>".
package conflicts

import (
	"fmt"
	"path"
	"time"
)

// Path returns the conflict-copy rel_path for relPath.
func Path(relPath, machineName string, t time.Time) string {
	dir := path.Dir(relPath)
	base := path.Base(relPath)
	ext := path.Ext(base)
	stem := base[:len(base)-len(ext)]
	name := fmt.Sprintf("%s (conflict %s %s)%s", stem, machineName, t.Format("2006-01-02_150405"), ext)
	if dir == "." {
		return name
	}
	return dir + "/" + name
}
