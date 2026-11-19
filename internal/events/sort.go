package events

import "sort"

func sortNewestFirst(evs []Event) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time.After(evs[j].Time) })
}
