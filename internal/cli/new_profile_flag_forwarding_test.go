package cli

import (
	"flag"
)

// newProfileRejectedByDesign names team-init flags that `new profile` refuses on
// purpose rather than forwards. --profile is set from the positional NAME, so
// passing it is an explicit usage error, not a forwarding gap.
var newProfileRejectedByDesign = map[string]bool{"profile": true}

// flagTakesSeparateValue reports whether a flag consumes the following argument.
// Go's flag package folds booleans into `-name` form, so only non-boolean flags
// take a separate value.
func flagTakesSeparateValue(f *flag.Flag) bool {
	bv, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !(ok && bv.IsBoolFlag())
}
