//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework WebKit
#include <stdlib.h>
#include "notch_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

func createNotchWindow(document string) error {
	value := C.CString(document)
	defer C.free(unsafe.Pointer(value))
	C.createNotchWindowC(value)
	return nil
}

func destroyNotchWindow() {
	C.destroyNotchWindowC()
}

func nativeShowNotchEvent(event NotchEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	value := C.CString(string(payload))
	defer C.free(unsafe.Pointer(value))
	C.showNotchEventC(value)
	return nil
}

func nativeClearNotch(id string) error {
	value := C.CString(id)
	defer C.free(unsafe.Pointer(value))
	C.clearNotchEventC(value)
	return nil
}

//export magenticNotchResponse
func magenticNotchResponse(payload *C.char) {
	value := C.GoString(payload)
	go dispatchNotchResponse(value)
}
