package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"magentic/remote"
)

// cliServeRemote startet den Host-Dienst aus Abschnitt 3: explizites Opt-in,
// Default aus, kein Port ohne dieses Subcommand. Die TUI bleibt unberührt —
// der Dienst läuft kopflos daneben und koordiniert sich über dieselbe
// Registry (ADR 0002).
func cliServeRemote(args []string) {
	fs := flag.NewFlagSet("serve-remote", flag.ExitOnError)
	bind := fs.String("bind", remote.DefaultBind, "Adresse, an die sich der Host bindet (Loopback oder Overlay wie Tailscale/LAN)")
	port := fs.Int("port", 8443, "Port des Host-Dienstes (nur TLS)")
	allow := fs.String("allow", "", "kommagetrennte beschränkte Methoden mit Host-Opt-in (z.B. RemoveWorktree,KillSession)")
	issue := fs.Bool("issue-token", false, "eine frische Geräte-Anmeldedaten ausgeben und beenden")
	revoke := fs.String("revoke-token", "", "eine Geräte-Anmeldedaten widerrufen und beenden (Wert wird nicht geloggt)")
	fs.Parse(args)

	if *revoke != "" {
		host, err := remote.NewHost(remote.HostConfig{
			Dir: remote.HostDir(), Bind: *bind, Port: *port,
			Backend: remote.NewCoreBackend(),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := host.Revoke(remote.HostToken(*revoke)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Anmeldedaten widerrufen; ihre Streams wurden geschlossen.")
		return
	}

	optIn := map[string]bool{}
	for _, name := range strings.Split(*allow, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		entry, known := remote.Classify(name)
		if !known || entry.Class != remote.ActionRestricted {
			fmt.Fprintf(os.Stderr, "keine opt-in-fähige beschränkte Methode: %q\n", name)
			os.Exit(2)
		}
		optIn[name] = true
	}

	host, err := remote.NewHost(remote.HostConfig{
		Dir: remote.HostDir(), Bind: *bind, Port: *port,
		// Als gesetzt gilt, was vom Default abweicht: Eine nicht-loopback
		// Bindung braucht immer --bind und ist damit automatisch explizit.
		BindExplicit: *bind != remote.DefaultBind || *port != 8443,
		Backend:      remote.NewCoreBackend(),
		OptIn:        optIn,
		Log:          func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer host.Close()

	if *issue {
		token, err := host.IssueToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("HostToken (einmalig — jetzt im Client ablegen, danach nie wieder lesbar):")
		fmt.Println(string(token))
		return
	}

	fmt.Printf("Host-Dienst hört auf %s (nur TLS, nur mit HostToken).\n", host.Addr())
	fmt.Println("Beenden mit Strg-C. Sessions laufen in tmux weiter.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = host.Close()
	}()
	if err := host.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
