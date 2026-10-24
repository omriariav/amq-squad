//go:build !darwin && !linux

package procinfo

func readStartTokenNative(pid int) (string, bool) { return "", false }

func readParentNative(pid int) (int, bool) { return 0, false }

func socketPeerNative(fd int) (int, bool) { return 0, false }
