package fs

import (
	"fmt"
	"path"
	"strings"
)

type claimsZonePath struct{ relative string }

func claimZonePath(name string) claimsZonePath {
	clean := path.Clean(name)
	if clean != name || !strings.HasPrefix(clean, claimDirectory+"/") || !safeClaimSourcePath(clean) {
		panic(fmt.Sprintf("fs store: non-claims terminal unlink %q", name))
	}
	return claimsZonePath{relative: strings.TrimPrefix(clean, claimDirectory+"/")}
}
