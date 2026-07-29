package main

import (
	"reflect"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	in := "Siehe [Doku](https://example.com/a) und **https://foo.bar/x**, dann http://localhost:5173.\n" +
		"Code: `https://code.example/y` und (https://z.de/p). Kein Link: https:// oder http://x"
	want := []string{
		"https://example.com/a",
		"https://foo.bar/x",
		"http://localhost:5173",
		"https://code.example/y",
		"https://z.de/p",
	}
	got := extractURLs(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractURLs:\n got %v\nwant %v", got, want)
	}
}
