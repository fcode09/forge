package artifact

import (
	"fmt"
	"strings"
)

// SafeRelPath reports whether p is a relative path made only of letters, digits and "._-/" with no ".."
// segment. Paths read from a manifest or checksums file must pass it before they are used on a file system.
func SafeRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return fmt.Errorf("path %q must be relative and not start with '-'", p)
	}
	for _, r := range p {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == '/'
		if !ok {
			return fmt.Errorf("path %q contains a character that is not allowed", p)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("path %q has an empty, '.' or '..' segment", p)
		}
	}
	return nil
}
