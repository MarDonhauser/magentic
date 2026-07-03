package core

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func ShortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func FormatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "jetzt"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func FormatAgeWord(t time.Time) string {
	s := FormatAge(t)
	if s == "jetzt" {
		return s
	}
	return "vor " + s
}

func ShortWeekday(t time.Time) string {
	days := map[time.Weekday]string{
		time.Monday: "Mo", time.Tuesday: "Di", time.Wednesday: "Mi",
		time.Thursday: "Do", time.Friday: "Fr", time.Saturday: "Sa", time.Sunday: "So",
	}
	return days[t.Weekday()] + " " + t.Format("15:04")
}

func SanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

func EnvWithout(key string) []string {
	prefix := key + "="
	env := os.Environ()
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}
