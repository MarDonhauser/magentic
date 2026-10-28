//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include "updates_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

func nativeUpdateStatus() UpdateStatus {
	value := C.updatesStatusC()
	defer C.free(unsafe.Pointer(value))
	var status UpdateStatus
	if err := json.Unmarshal([]byte(C.GoString(value)), &status); err != nil {
		return UpdateStatus{Version: "Entwicklungsbuild", Reason: "Der Update-Status konnte nicht gelesen werden. Bitte starte die App neu."}
	}
	return status
}

func nativeCheckForUpdates() error {
	value := C.checkForUpdatesC()
	defer C.free(unsafe.Pointer(value))
	if message := C.GoString(value); message != "" {
		return errors.New(message)
	}
	return nil
}

func installUpdaterMenu() {
	C.installUpdaterMenuC()
}
