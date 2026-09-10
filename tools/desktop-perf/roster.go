package main

import (
	"fmt"
	"sort"
)

// Freeze group membership so helpers cannot silently disappear from totals.
func resolveGroups(snapshot []processInfo, roots groups, children bool) ([]processInfo, error) {
	byID := map[uint32]processInfo{}
	for _, p := range snapshot {
		byID[p.PID] = p
	}
	owners := map[uint32]string{}
	for name, ids := range roots {
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return nil, fmt.Errorf("PID %d not found", id)
			}
			if old, ok := owners[id]; ok && old != name {
				return nil, fmt.Errorf("PID %d belongs to %s and %s", id, old, name)
			}
			owners[id] = name
		}
	}
	if children {
		for changed := true; changed; {
			changed = false
			for _, p := range snapshot {
				owner, ok := owners[p.Parent]
				if !ok || p.PID == p.Parent {
					continue
				}
				if old, ok := owners[p.PID]; ok {
					if old != owner {
						return nil, fmt.Errorf("overlapping trees: PID %d belongs to %s and %s", p.PID, old, owner)
					}
					continue
				}
				owners[p.PID] = owner
				changed = true
			}
		}
	}
	result := make([]processInfo, 0, len(owners))
	for id, name := range owners {
		p := byID[id]
		p.Group = name
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result, nil
}
