// Package cwm holds files that live at the module root and must be
// embedded elsewhere: the LICENSE text, read by internal/buildinfo to
// report its SPDX id (D69). The commands are under cmd/.
package cwm

import _ "embed"

// License is the repository's LICENSE file, verbatim.
//
//go:embed LICENSE
var License string
