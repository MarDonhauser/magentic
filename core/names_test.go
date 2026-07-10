package core

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix login redirect bug in Keycloak":       "fix-login-redirect",
		"der Button auf der Startseite ist kaputt": "button-startseite-kaputt",
		"/deploy req.pilot ":                       "deploy-req-pilot",
		"Übersicht für Deploys":                    "uebersicht-deploys",
		"und der die das":                          "und-der-die",
		"":                                         "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
