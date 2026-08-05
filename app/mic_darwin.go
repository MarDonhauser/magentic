//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreAudio
#include <CoreAudio/CoreAudio.h>

// 1, wenn irgendein Prozess das Standard-Eingabegerät offen hält — während
// eines Teams/Zoom-Calls durchgehend der Fall, auch stummgeschaltet.
static int micInUseC() {
	AudioObjectPropertyAddress addr = {
		kAudioHardwarePropertyDefaultInputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain,
	};
	AudioDeviceID dev = kAudioObjectUnknown;
	UInt32 size = sizeof(dev);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &addr, 0, NULL, &size, &dev) != noErr || dev == kAudioObjectUnknown) {
		return 0;
	}
	AudioObjectPropertyAddress run = {
		kAudioDevicePropertyDeviceIsRunningSomewhere,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain,
	};
	UInt32 running = 0;
	size = sizeof(running);
	if (AudioObjectGetPropertyData(dev, &run, 0, NULL, &size, &running) != noErr) {
		return 0;
	}
	return running != 0;
}
*/
import "C"

func micInUse() bool {
	return C.micInUseC() == 1
}
