//go:build windows

package purego

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isAccessDenied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
