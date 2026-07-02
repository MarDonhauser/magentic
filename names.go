package main

import "fmt"

var agentNames = []string{
	"atlas", "hera", "nyx", "orion", "iris", "helios", "rhea", "eos",
	"argo", "kato", "leda", "milo", "nero", "otis", "pax", "remy",
	"selene", "theo", "vega", "wren", "zeno", "juno", "kai", "luna",
	"mars", "nova", "odin", "pia", "quill", "rex", "sol", "tara",
}

func PickAgentName(s *State) string {
	for _, n := range agentNames {
		if !s.HasAgent(n) && !TmuxHasSession(tmuxSessionName(n)) {
			return n
		}
	}
	for i := 2; ; i++ {
		for _, n := range agentNames {
			candidate := fmt.Sprintf("%s-%d", n, i)
			if !s.HasAgent(candidate) && !TmuxHasSession(tmuxSessionName(candidate)) {
				return candidate
			}
		}
	}
}
