package main

import (
	"reflect"
	"testing"

	"magentic/remote"
)

// Jede Interface-Methode muss in der kanonischen Methodenliste stehen und in
// der RemoteActionPolicy klassifiziert sein — sonst könnte eine neue
// Binding-Methode ohne Policy-Entscheidung veröffentlicht werden.
func TestHostAPIClassifiedInPolicy(t *testing.T) {
	iface := reflect.TypeOf((*HostAPI)(nil)).Elem()
	methods := map[string]bool{}
	for _, name := range remote.HostAPIMethods {
		methods[name] = true
	}
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		if !methods[name] {
			t.Errorf("HostAPI.%s fehlt in remote.HostAPIMethods", name)
		}
		if _, known := remote.Classify(name); !known {
			t.Errorf("HostAPI.%s ist nicht in remote.RemoteActionPolicy klassifiziert", name)
		}
	}
	for _, name := range remote.HostAPIMethods {
		if _, ok := iface.MethodByName(name); !ok {
			t.Errorf("remote.HostAPIMethods nennt %s, aber HostAPI hat die Methode nicht", name)
		}
	}
}

// Jede heute gebundene *App-Methode muss über die Nahtstelle laufen: Was
// Wails sieht, sieht auch der Host.
func TestHostAPICoversBoundMethods(t *testing.T) {
	iface := reflect.TypeOf((*HostAPI)(nil)).Elem()
	covered := map[string]bool{}
	for i := 0; i < iface.NumMethod(); i++ {
		covered[iface.Method(i).Name] = true
	}
	app := reflect.TypeOf((*App)(nil))
	for i := 0; i < app.NumMethod(); i++ {
		name := app.Method(i).Name
		if !covered[name] {
			t.Errorf("*App.%s ist gebunden, aber nicht in HostAPI enthalten", name)
		}
	}
}
