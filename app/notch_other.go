//go:build !darwin

package main

func createNotchWindow(document string) error { return nil }
func destroyNotchWindow()                     {}
func nativeShowNotchEvent(event NotchEvent) error {
	return nil
}
func nativeClearNotch(id string) error { return nil }
func nativeAcknowledgeNotch()          {}
