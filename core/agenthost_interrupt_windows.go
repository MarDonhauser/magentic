//go:build windows

package core

import "errors"

// interruptVendorTurn has no Windows implementation: the managed runtime is
// refused on unsupported platforms with the reason stated (see the
// service-installation spec), so no managed Session ever reaches this path.
func interruptVendorTurn(int) error {
	return errors.New("der managed Runtime unterstützt kein Unterbrechen auf Windows")
}
