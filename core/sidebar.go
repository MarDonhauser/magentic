package core

import (
	"fmt"
	"sort"
	"strings"
)

// SidebarRef addresses one entry of the session list without carrying its
// payload, so an order can be transported as a list of references.
type SidebarRef struct {
	Kind SidebarSlotKind `json:"kind"`
	Ref  string          `json:"ref"`
}

// SidebarNode is one resolved row of the session list, children included.
// Resolution happens here rather than in the UI so placement rules exist once.
type SidebarNode struct {
	Kind      SidebarSlotKind `json:"kind"`
	Ref       string          `json:"ref"`
	Name      string          `json:"name,omitempty"`
	Collapsed bool            `json:"collapsed,omitempty"`
	Children  []SidebarNode   `json:"children,omitempty"`
}

// sidebarListed reports whether a Session belongs in the session list at all.
// Dock terminals are tooling and sessions closed for later live in their own
// block below the list.
func sidebarListed(session Session) bool {
	return session.LaterAt.IsZero() && !session.IsDock()
}

// SidebarLayout resolves slots and defaults into the rows the session list
// shows, in order. Within one level the placed entries come first in their slot
// order, then everything that kept its default placement.
func SidebarLayout(s *State) []SidebarNode {
	if s == nil {
		return nil
	}
	placement := map[SidebarRef]*SidebarSlot{}
	for i := range s.Sidebar {
		placement[SidebarRef{Kind: s.Sidebar[i].Kind, Ref: s.Sidebar[i].Ref}] = &s.Sidebar[i]
	}

	// Default members per level, in the order the registry stores them.
	top := []SidebarRef{}
	for _, divider := range s.Sidebar {
		if divider.Kind == SidebarSlotDivider {
			top = append(top, SidebarRef{Kind: SidebarSlotDivider, Ref: divider.Ref})
		}
	}
	projectOf := map[string][]SidebarRef{}
	for _, project := range s.Projects {
		ref := SidebarRef{Kind: SidebarSlotProject, Ref: string(project.ID)}
		if slot := placement[ref]; slot == nil || slot.TopLevel() {
			top = append(top, ref)
		}
	}
	for _, session := range s.Agents {
		if !sidebarListed(session) {
			continue
		}
		ref := SidebarRef{Kind: SidebarSlotSession, Ref: string(session.ID)}
		slot := placement[ref]
		switch {
		case slot == nil, slot.ParentKind == SidebarSlotProject:
			projectOf[string(session.ProjectID)] = append(projectOf[string(session.ProjectID)], ref)
		case slot.TopLevel():
			top = append(top, ref)
		}
	}

	inDivider := map[string][]SidebarRef{}
	for _, slot := range s.Sidebar {
		if slot.ParentKind == SidebarSlotDivider {
			inDivider[slot.Parent] = append(inDivider[slot.Parent], SidebarRef{Kind: slot.Kind, Ref: slot.Ref})
		}
	}

	var build func(members []SidebarRef, parentKind SidebarSlotKind, parent string) []SidebarNode
	build = func(members []SidebarRef, parentKind SidebarSlotKind, parent string) []SidebarNode {
		rank := map[SidebarRef]int{}
		for i, slot := range s.Sidebar {
			if slot.ParentKind == parentKind && slot.Parent == parent {
				rank[SidebarRef{Kind: slot.Kind, Ref: slot.Ref}] = i
			}
		}
		sort.SliceStable(members, func(i, j int) bool {
			ri, iok := rank[members[i]]
			rj, jok := rank[members[j]]
			if iok != jok {
				return iok
			}
			return iok && ri < rj
		})
		nodes := make([]SidebarNode, 0, len(members))
		for _, ref := range members {
			node := SidebarNode{Kind: ref.Kind, Ref: ref.Ref}
			if slot := placement[ref]; slot != nil {
				node.Name = slot.Name
				node.Collapsed = slot.Collapsed
			}
			switch ref.Kind {
			case SidebarSlotDivider:
				node.Children = build(append([]SidebarRef(nil), inDivider[ref.Ref]...), SidebarSlotDivider, ref.Ref)
			case SidebarSlotProject:
				node.Children = build(append([]SidebarRef(nil), projectOf[ref.Ref]...), SidebarSlotProject, ref.Ref)
			}
			nodes = append(nodes, node)
		}
		return nodes
	}
	return build(top, "", "")
}

// sidebarMayHold reports whether an entry of that kind is allowed to sit in
// that level. Dividers stay at the top, projects reach one level deep, and a
// session may only enter the project it actually belongs to.
func sidebarMayHold(s *State, kind SidebarSlotKind, ref string, parentKind SidebarSlotKind, parent string) error {
	switch kind {
	case SidebarSlotDivider:
		if s.SidebarDivider(DividerID(ref)) == nil {
			return fmt.Errorf("unbekannter Divider: %s", ref)
		}
		if parentKind != "" {
			return fmt.Errorf("ein Divider gehört auf die oberste Ebene")
		}
	case SidebarSlotProject:
		if s.ProjectByID(ProjectID(ref)) == nil {
			return fmt.Errorf("unbekanntes Projekt: %s", ref)
		}
		if parentKind != "" && parentKind != SidebarSlotDivider {
			return fmt.Errorf("ein Projekt gehört auf die oberste Ebene oder in einen Divider")
		}
	case SidebarSlotSession:
		session := s.SessionByID(SessionID(ref))
		if session == nil {
			return fmt.Errorf("unbekannte Session: %s", ref)
		}
		if parentKind == SidebarSlotProject && string(session.ProjectID) != parent {
			return fmt.Errorf("Session %q gehört nicht zu diesem Projekt", session.Name)
		}
		if parentKind != "" && parentKind != SidebarSlotDivider && parentKind != SidebarSlotProject {
			return fmt.Errorf("ungültige Ablage für eine Session")
		}
	default:
		return fmt.Errorf("unbekannte Sidebar-Art: %q", kind)
	}
	if parentKind == SidebarSlotDivider && s.SidebarDivider(DividerID(parent)) == nil {
		return fmt.Errorf("unbekannter Divider: %s", parent)
	}
	if parentKind == SidebarSlotProject && s.ProjectByID(ProjectID(parent)) == nil {
		return fmt.Errorf("unbekanntes Projekt: %s", parent)
	}
	return nil
}

// SidebarDivider finds a divider slot by ID.
func (s *State) SidebarDivider(id DividerID) *SidebarSlot {
	if id == "" {
		return nil
	}
	for i := range s.Sidebar {
		if s.Sidebar[i].Kind == SidebarSlotDivider && s.Sidebar[i].Ref == string(id) {
			return &s.Sidebar[i]
		}
	}
	return nil
}

func AddDivider(id DividerID, name string) RegistryChange {
	return RegistryChange{kind: registryAddDivider, sidebar: sidebarChange{ref: string(id), name: strings.TrimSpace(name)}}
}

func RenameDivider(id DividerID, name string) RegistryChange {
	return RegistryChange{kind: registryRenameDivider, sidebar: sidebarChange{ref: string(id), name: strings.TrimSpace(name)}}
}

func RemoveDivider(id DividerID) RegistryChange {
	return RegistryChange{kind: registryRemoveDivider, sidebar: sidebarChange{ref: string(id)}}
}

func SetDividerCollapsed(id DividerID, collapsed bool) RegistryChange {
	return RegistryChange{kind: registrySetDividerCollapsed, sidebar: sidebarChange{ref: string(id), collapsed: collapsed}}
}

// MoveSidebarItem places one entry into a level and rewrites that level's order
// to exactly order. The caller sends the level as it wants to see it, which
// keeps the placement of entries that never had a slot expressible.
func MoveSidebarItem(kind SidebarSlotKind, ref string, parentKind SidebarSlotKind, parent string, order []SidebarRef) RegistryChange {
	return RegistryChange{kind: registryMoveSidebarItem, sidebar: sidebarChange{
		kind:       kind,
		ref:        ref,
		parentKind: parentKind,
		parent:     parent,
		order:      append([]SidebarRef(nil), order...),
	}}
}

func applySidebarChange(state *State, change RegistryChange) (bool, error) {
	c := change.sidebar
	switch change.kind {
	case registryAddDivider:
		if c.ref == "" {
			return false, fmt.Errorf("Divider ohne ID")
		}
		if c.name == "" {
			return false, fmt.Errorf("Divider braucht einen Namen")
		}
		if state.SidebarDivider(DividerID(c.ref)) != nil {
			return false, fmt.Errorf("Divider %s existiert schon", c.ref)
		}
		state.Sidebar = append(state.Sidebar, SidebarSlot{Kind: SidebarSlotDivider, Ref: c.ref, Name: c.name})
		return true, nil
	case registryRenameDivider:
		divider := state.SidebarDivider(DividerID(c.ref))
		if divider == nil {
			return false, fmt.Errorf("unbekannter Divider: %s", c.ref)
		}
		if c.name == "" {
			return false, fmt.Errorf("Divider braucht einen Namen")
		}
		if divider.Name == c.name {
			return false, nil
		}
		divider.Name = c.name
		return true, nil
	case registrySetDividerCollapsed:
		divider := state.SidebarDivider(DividerID(c.ref))
		if divider == nil {
			return false, fmt.Errorf("unbekannter Divider: %s", c.ref)
		}
		if divider.Collapsed == c.collapsed {
			return false, nil
		}
		divider.Collapsed = c.collapsed
		return true, nil
	case registryRemoveDivider:
		if state.SidebarDivider(DividerID(c.ref)) == nil {
			return false, fmt.Errorf("unbekannter Divider: %s", c.ref)
		}
		kept := state.Sidebar[:0]
		for _, slot := range state.Sidebar {
			if slot.Kind == SidebarSlotDivider && slot.Ref == c.ref {
				continue
			}
			// Children stay where they are and surface at the top level.
			if slot.ParentKind == SidebarSlotDivider && slot.Parent == c.ref {
				slot.ParentKind = ""
				slot.Parent = ""
			}
			kept = append(kept, slot)
		}
		state.Sidebar = kept
		return true, nil
	case registryMoveSidebarItem:
		return applySidebarMove(state, c)
	}
	return false, fmt.Errorf("unbekannte Sidebar-Änderung")
}

func applySidebarMove(state *State, c sidebarChange) (bool, error) {
	if err := sidebarMayHold(state, c.kind, c.ref, c.parentKind, c.parent); err != nil {
		return false, err
	}
	moved := SidebarRef{Kind: c.kind, Ref: c.ref}

	seen := map[SidebarRef]bool{}
	found := false
	for _, ref := range c.order {
		if seen[ref] {
			return false, fmt.Errorf("doppelter Eintrag in der Sortierung: %s", ref.Ref)
		}
		seen[ref] = true
		if err := sidebarMayHold(state, ref.Kind, ref.Ref, c.parentKind, c.parent); err != nil {
			return false, err
		}
		if ref == moved {
			found = true
		}
	}
	if !found {
		return false, fmt.Errorf("der verschobene Eintrag fehlt in der Sortierung")
	}

	before := append([]SidebarSlot(nil), state.Sidebar...)
	// Carry a divider's own name and collapse state across the rewrite.
	keep := map[SidebarRef]SidebarSlot{}
	for _, slot := range state.Sidebar {
		keep[SidebarRef{Kind: slot.Kind, Ref: slot.Ref}] = slot
	}
	kept := make([]SidebarSlot, 0, len(state.Sidebar))
	for _, slot := range state.Sidebar {
		ref := SidebarRef{Kind: slot.Kind, Ref: slot.Ref}
		if seen[ref] {
			continue
		}
		// Anything left at the target level but absent from the new order
		// gives up its placement and falls back to the default one.
		if slot.ParentKind == c.parentKind && slot.Parent == c.parent && slot.Kind != SidebarSlotDivider {
			continue
		}
		kept = append(kept, slot)
	}
	for _, ref := range c.order {
		slot := SidebarSlot{Kind: ref.Kind, Ref: ref.Ref, ParentKind: c.parentKind, Parent: c.parent}
		if old, ok := keep[ref]; ok {
			slot.Name = old.Name
			slot.Collapsed = old.Collapsed
		}
		kept = append(kept, slot)
	}
	state.Sidebar = kept
	normalizeSidebar(state)
	return !sameSidebar(before, state.Sidebar), nil
}

func sameSidebar(a, b []SidebarSlot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// normalizeSidebar drops slots whose entry is gone and regroups children right
// behind their parent, so the stored order can be read top to bottom.
func normalizeSidebar(state *State) bool {
	before := append([]SidebarSlot(nil), state.Sidebar...)
	dividers := map[string]bool{}
	for _, slot := range state.Sidebar {
		if slot.Kind == SidebarSlotDivider && slot.Ref != "" && slot.Name != "" {
			dividers[slot.Ref] = true
		}
	}
	alive := make([]SidebarSlot, 0, len(state.Sidebar))
	seen := map[SidebarRef]bool{}
	for _, slot := range state.Sidebar {
		ref := SidebarRef{Kind: slot.Kind, Ref: slot.Ref}
		if slot.Ref == "" || seen[ref] {
			continue
		}
		switch slot.Kind {
		case SidebarSlotDivider:
			if slot.Name == "" {
				continue
			}
			slot.ParentKind, slot.Parent = "", ""
		case SidebarSlotProject:
			if state.ProjectByID(ProjectID(slot.Ref)) == nil {
				continue
			}
		case SidebarSlotSession:
			session := state.SessionByID(SessionID(slot.Ref))
			if session == nil || !sidebarListed(*session) {
				continue
			}
			if slot.ParentKind == SidebarSlotProject && string(session.ProjectID) != slot.Parent {
				slot.ParentKind, slot.Parent = "", ""
			}
		default:
			continue
		}
		if slot.ParentKind == SidebarSlotDivider && !dividers[slot.Parent] {
			slot.ParentKind, slot.Parent = "", ""
		}
		if slot.ParentKind == SidebarSlotProject && state.ProjectByID(ProjectID(slot.Parent)) == nil {
			slot.ParentKind, slot.Parent = "", ""
		}
		seen[ref] = true
		alive = append(alive, slot)
	}

	ordered := make([]SidebarSlot, 0, len(alive))
	var emit func(parentKind SidebarSlotKind, parent string)
	emit = func(parentKind SidebarSlotKind, parent string) {
		for _, slot := range alive {
			if slot.ParentKind != parentKind || slot.Parent != parent {
				continue
			}
			ordered = append(ordered, slot)
			if slot.Kind == SidebarSlotDivider {
				emit(SidebarSlotDivider, slot.Ref)
			} else if slot.Kind == SidebarSlotProject {
				emit(SidebarSlotProject, slot.Ref)
			}
		}
	}
	emit("", "")
	if len(ordered) == 0 {
		ordered = nil
	}
	state.Sidebar = ordered
	return !sameSidebar(before, state.Sidebar)
}

// migrateProjectOrderToSidebar turns the pre-schema-3 project order into
// explicit top-level slots, so an existing arrangement survives the upgrade.
func migrateProjectOrderToSidebar(state *State) bool {
	if len(state.Sidebar) > 0 || len(state.Projects) == 0 {
		return false
	}
	for _, project := range state.Projects {
		if project.ID == "" {
			continue
		}
		state.Sidebar = append(state.Sidebar, SidebarSlot{Kind: SidebarSlotProject, Ref: string(project.ID)})
	}
	return len(state.Sidebar) > 0
}

func validateSidebar(state *State) error {
	seen := map[SidebarRef]bool{}
	for _, slot := range state.Sidebar {
		ref := SidebarRef{Kind: slot.Kind, Ref: slot.Ref}
		if slot.Ref == "" {
			return fmt.Errorf("Registry enthält einen Sidebar-Eintrag ohne Ziel")
		}
		if seen[ref] {
			return fmt.Errorf("Registry enthält einen doppelten Sidebar-Eintrag: %s", slot.Ref)
		}
		seen[ref] = true
		if slot.Kind == SidebarSlotDivider {
			if slot.Name == "" {
				return fmt.Errorf("Registry enthält einen Divider ohne Namen")
			}
			if !slot.TopLevel() {
				return fmt.Errorf("Divider %q liegt nicht auf der obersten Ebene", slot.Name)
			}
			continue
		}
		if err := sidebarMayHold(state, slot.Kind, slot.Ref, slot.ParentKind, slot.Parent); err != nil {
			return fmt.Errorf("Registry enthält eine ungültige Sidebar-Ablage: %w", err)
		}
	}
	return nil
}
