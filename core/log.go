package core

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func Logf(format string, args ...any) {
	p := filepath.Join(filepath.Dir(StatePath()), "app.log")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
}
