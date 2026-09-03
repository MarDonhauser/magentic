package core

// AgentVendorOption is what the UI needs to offer one vendor: its durable
// value, a human label, and whether its program is installed. A vendor whose
// binary is missing stays visible but unselectable, so the reason a Session
// cannot start is stated before it is attempted.
type AgentVendorOption struct {
	Vendor    string `json:"vendor"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

var agentVendorLabels = map[AgentVendor]string{
	AgentVendorClaude:      "Claude Code",
	AgentVendorCodex:       "Codex",
	AgentVendorGemini:      "Gemini CLI",
	AgentVendorCopilot:     "GitHub Copilot",
	AgentVendorAntigravity: "Antigravity",
}

// AgentVendorCatalog lists the selectable vendors in presentation order.
func AgentVendorCatalog() []AgentVendorOption {
	providers := builtinAgentProviders()
	catalog := make([]AgentVendorOption, 0, len(providers))
	for _, provider := range providers {
		label := agentVendorLabels[provider.Vendor()]
		if label == "" {
			label = string(provider.Vendor())
		}
		catalog = append(catalog, AgentVendorOption{
			Vendor:    string(provider.Vendor()),
			Label:     label,
			Available: providerBinaryAvailable(provider),
		})
	}
	return catalog
}
