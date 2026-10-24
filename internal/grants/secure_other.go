//go:build !unix

package grants

// Insecure has no permission model to check off unix; the owner file counts
// as trusted (3.0 hosts are macOS, plan 8).
func Insecure(path string) string { return "" }
