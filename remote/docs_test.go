package remote

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Die dokumentierten Policy-Defaults stimmen mit den erzwungenen überein:
// Jede policy-Markierung in der README muss die Tabelle treffen, und die
// vier kritischen Beschränkungen plus die erlaubte Kernfläche müssen
// dokumentiert sein. Doku und Durchsetzung laufen so nicht auseinander.
func TestDocumentedPolicyMatchesEnforcement(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	marker := regexp.MustCompile(`<!-- policy: (\w+) (permitted|restricted) -->`)
	matches := marker.FindAllSubmatch(readme, -1)
	if len(matches) == 0 {
		t.Fatal("keine Policy-Marker in der README gefunden")
	}
	documented := map[string]bool{}
	for _, match := range matches {
		method, class := string(match[1]), ActionClass(match[2])
		documented[method] = true
		entry, known := Classify(method)
		if !known {
			t.Errorf("README nennt %s, Policy kennt es nicht", method)
			continue
		}
		if entry.Class != class {
			t.Errorf("README sagt %s=%s, erzwungen wird %s", method, class, entry.Class)
		}
		if err := EnforceRemote(method, nil); (err == nil) != (class == ActionPermitted) {
			t.Errorf("Durchsetzung für %s weicht von der README ab", method)
		}
	}
	for _, method := range []string{
		"Overview", "SendMessage", "NewSession", "OpenTerm",
		"RemoveWorktree", "RemoveProject", "AddProject", "KillSession",
	} {
		if !documented[method] {
			t.Errorf("Kernfläche %s ist nicht dokumentiert", method)
		}
	}
	// Die Browser-Client-Entscheidung (Open Question) steht geschrieben.
	for _, want := range []string{"Browser-Client", "wailsjs/runtime", "Folgestufe"} {
		if !containsString(string(readme), want) {
			t.Errorf("Browser-Entscheidung fehlt in der README (%q)", want)
		}
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
