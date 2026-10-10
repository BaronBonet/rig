package tui

import (
	"slices"
	"strings"
	"time"
)

// orderKey is what places a row, a family of rows or a repo group in the
// list: whether work is in progress and how recently there was any, and, so
// that ties always fall the same way, when the Task was created and its ID.
type orderKey struct {
	working bool
	// active is the last activity: the newest of the Task's status, Activity
	// events, record update and creation.
	active  time.Time
	created time.Time
	id      string
}

// compareOrderKeys sorts working keys before the others and, among alike,
// the most recently active first. Ties go to the Task created first, as the
// list was ordered before, then to the lower ID, so equal inputs never
// reorder between refreshes.
func compareOrderKeys(a, b orderKey) int {
	if a.working != b.working {
		if a.working {
			return -1
		}
		return 1
	}
	if byActivity := b.active.Compare(a.active); byActivity != 0 {
		return byActivity
	}
	if byCreation := a.created.Compare(b.created); byCreation != 0 {
		return byCreation
	}
	return strings.Compare(a.id, b.id)
}

// freezeOrder fixes where the row ranks, from what is known of it now. The
// list is only arranged afresh when it is opened, refreshed or switched to
// another view; until then a live status or Activity event changes what the
// row shows but not where it is, so the rank it was given is the one every
// arrangement uses.
func (r *taskRow) freezeOrder() {
	r.order = rowOrderKey(*r)
}

// rowOrderKey ranks one row from its live state. Its last activity falls back
// through the Task's record update to its creation, so a row whose status has
// not loaded yet still has a place.
func rowOrderKey(row taskRow) orderKey {
	key := orderKey{id: taskID(row.task)}
	if row.status != nil {
		key.working = row.status.Phase.IsWorking()
		key.active = row.status.ObservedAt
	}
	for _, event := range row.activity {
		key.active = laterOf(key.active, event.ObservedAt)
	}
	if row.task != nil {
		key.active = laterOf(key.active, row.task.UpdatedAt)
		key.active = laterOf(key.active, row.task.CreatedAt)
		key.created = row.task.CreatedAt
	}
	return key
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// rowFamily is a Task with the Child tasks listed under it, kept together
// however they rank. A Child task whose parent is not listed is a family of
// its own.
type rowFamily struct {
	rows []taskRow
	// key is the best of its members' ranks, led by the parent's creation and
	// ID: one working session lifts the whole family.
	key orderKey
}

// repoGroup is the families of one repository, listed under its header.
type repoGroup struct {
	families []rowFamily
	// key is its best family's, which comes first.
	key orderKey
}

// orderRows is the single order the task list is shown in, for the current
// list, the shelf and all folders alike: repo groups, each led by its best
// family; inside them families, each led by its Task with its Child tasks
// under it. Working rows rank before the rest and, among alike, the most
// recently active first, so the work happening when the list was arranged is
// at the top. It arranges the rows by their frozen ranks, never their live
// state, so rows already listed keep their order relative to each other
// whenever it runs. It returns a new slice and leaves rows as they were.
func orderRows(rows []taskRow) []taskRow {
	repoKeys := make([]string, 0, len(rows))
	byRepo := make(map[string][]taskRow, len(rows))
	for _, row := range rows {
		repoKey := repoGroupKey(row.task)
		if _, ok := byRepo[repoKey]; !ok {
			repoKeys = append(repoKeys, repoKey)
		}
		byRepo[repoKey] = append(byRepo[repoKey], row)
	}

	groups := make([]repoGroup, 0, len(repoKeys))
	for _, repoKey := range repoKeys {
		groups = append(groups, newRepoGroup(byRepo[repoKey]))
	}
	slices.SortStableFunc(groups, func(a, b repoGroup) int { return compareOrderKeys(a.key, b.key) })

	ordered := make([]taskRow, 0, len(rows))
	for _, group := range groups {
		for _, family := range group.families {
			ordered = append(ordered, family.rows...)
		}
	}
	return ordered
}

func newRepoGroup(rows []taskRow) repoGroup {
	families := newRowFamilies(rows)
	slices.SortStableFunc(families, func(a, b rowFamily) int { return compareOrderKeys(a.key, b.key) })
	return repoGroup{families: families, key: families[0].key}
}

// newRowFamilies nests each Child task under its parent. Only a Task that is
// not itself a Child task can be a parent, so every listed row is in exactly
// one family.
func newRowFamilies(rows []taskRow) []rowFamily {
	parents := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !row.task.IsChild() {
			parents[taskID(row.task)] = true
		}
	}

	families := make([]rowFamily, 0, len(rows))
	familyAt := make(map[string]int, len(rows))
	var children []taskRow
	for _, row := range rows {
		if row.task.IsChild() && parents[strings.TrimSpace(row.task.ParentID)] {
			children = append(children, row)
			continue
		}
		if _, ok := familyAt[taskID(row.task)]; !ok {
			familyAt[taskID(row.task)] = len(families)
		}
		families = append(families, rowFamily{rows: []taskRow{row}})
	}
	for _, child := range children {
		family := &families[familyAt[strings.TrimSpace(child.task.ParentID)]]
		family.rows = append(family.rows, child)
	}

	for index := range families {
		family := &families[index]
		parent, sessions := family.rows[0], family.rows[1:]
		slices.SortStableFunc(sessions, func(a, b taskRow) int { return compareOrderKeys(a.order, b.order) })
		family.key = parent.order
		for _, member := range sessions {
			family.key.working = family.key.working || member.order.working
			family.key.active = laterOf(family.key.active, member.order.active)
		}
	}
	return families
}
