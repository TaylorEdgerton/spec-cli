package main

import "strings"

// screenAction is an intent returned by the canonical screen model.
type screenAction string

const noScreenAction screenAction = ""

type screenItem struct {
	ID         string
	Label      string
	Selectable bool
	Action     screenAction
	Preview    any
}

type screenSection struct {
	ID          string
	Title       string
	Items       []screenItem
	EmptyReason string
}

type canonicalScreen struct {
	Sections []screenSection
	Cursor   int
}

func (screen canonicalScreen) visibleItemIDs() []string {
	var ids []string
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (screen canonicalScreen) selectableItems() []screenItem {
	var items []screenItem
	for _, section := range screen.Sections {
		for _, item := range section.Items {
			if item.Selectable {
				items = append(items, item)
			}
		}
	}
	return items
}

func (screen canonicalScreen) selectableItemIDs() []string {
	items := screen.selectableItems()
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func (screen *canonicalScreen) move(delta int) {
	count := len(screen.selectableItems())
	if count == 0 {
		screen.Cursor = 0
		return
	}
	screen.Cursor = wrap(screen.Cursor+delta, count)
}

func (screen canonicalScreen) selectedItem() (screenItem, bool) {
	items := screen.selectableItems()
	if len(items) == 0 {
		return screenItem{}, false
	}
	return items[clamp(screen.Cursor, 0, len(items)-1)], true
}

func (screen canonicalScreen) activate() screenAction {
	item, ok := screen.selectedItem()
	if !ok {
		return noScreenAction
	}
	return item.Action
}

func (screen canonicalScreen) body() string {
	selected, hasSelection := screen.selectedItem()
	sections := make([]string, 0, len(screen.Sections))
	for _, section := range screen.Sections {
		lines := []string{uiTitleStyle.Render(section.Title)}
		if len(section.Items) == 0 {
			lines = append(lines, uiEmptyState("", section.EmptyReason))
		} else {
			for _, item := range section.Items {
				line := "  " + item.Label
				if item.Selectable && hasSelection && item.ID == selected.ID {
					line = uiSelectedRow("> "+item.Label, 0)
				}
				lines = append(lines, line)
			}
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func (screen *canonicalScreen) key(keystroke string) screenAction {
	switch keystroke {
	case "up", "k":
		screen.move(-1)
	case "down", "j":
		screen.move(1)
	case "enter":
		return screen.activate()
	}
	return noScreenAction
}
