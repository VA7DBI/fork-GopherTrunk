//go:build !windows

package purego

func isAccessDenied(error) bool { return false }
