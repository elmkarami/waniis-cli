//go:build 386 || arm || mips || mipsle

package export

import (
	"errors"
	"io"
)

// XLSXSupported reports whether this build can write Excel files. The Excel library doesn't
// compile for 32-bit CPUs, so 32-bit builds export CSV only.
const XLSXSupported = false

// ErrXLSXUnsupported is returned by WriteXLSX on 32-bit builds.
var ErrXLSXUnsupported = errors.New("Excel export isn't available in 32-bit builds of waniis; use --format csv")

// WriteXLSX is unavailable on 32-bit builds; it always returns ErrXLSXUnsupported.
func WriteXLSX(_ io.Writer, _ Table) error { return ErrXLSXUnsupported }
