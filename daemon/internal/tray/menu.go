package tray

import "github.com/godbus/dbus/v5"

// menuItem is a com.canonical.dbusmenu layout node, (ia{sv}av).
type menuItem struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

type idProps struct {
	ID    int32
	Props map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	Event     string
	Data      dbus.Variant
	Timestamp uint32
}

var menuItems = []menuItem{
	{menuOpen, map[string]dbus.Variant{"label": dbus.MakeVariant("Open Wisp")}, []dbus.Variant{}},
	{menuQuit, map[string]dbus.Variant{"label": dbus.MakeVariant("Quit Wisp")}, []dbus.Variant{}},
}

func findItem(id int32) (menuItem, bool) {
	if id == 0 {
		root := menuItem{0, map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, nil}
		for _, it := range menuItems {
			root.Children = append(root.Children, dbus.MakeVariant(it))
		}
		return root, true
	}
	for _, it := range menuItems {
		if it.ID == id {
			return it, true
		}
	}
	return menuItem{}, false
}

var errNoItem = dbus.NewError(menuIface+".Error.NoSuchItem", []any{"no such menu item"})

// menu is the com.canonical.dbusmenu interface. The menu never changes, so
// the layout revision stays 1.
type menu struct{ t *Tray }

func (m menu) GetLayout(parent, depth int32, names []string) (uint32, menuItem, *dbus.Error) {
	it, ok := findItem(parent)
	if !ok {
		return 0, menuItem{}, errNoItem
	}
	return 1, it, nil
}

func (m menu) GetGroupProperties(ids []int32, names []string) ([]idProps, *dbus.Error) {
	out := []idProps{}
	for _, id := range ids {
		if it, ok := findItem(id); ok {
			out = append(out, idProps{id, it.Props})
		}
	}
	return out, nil
}

func (m menu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	it, ok := findItem(id)
	if v, has := it.Props[name]; ok && has {
		return v, nil
	}
	return dbus.Variant{}, errNoItem
}

func (m menu) Event(id int32, event string, data dbus.Variant, ts uint32) *dbus.Error {
	if event != "clicked" {
		return nil
	}
	switch id {
	case menuOpen:
		go m.t.open()
	case menuQuit:
		if m.t.cfg.Quit != nil {
			go m.t.cfg.Quit()
		}
	}
	return nil
}

func (m menu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		m.Event(e.ID, e.Event, e.Data, e.Timestamp)
	}
	return []int32{}, nil
}

func (m menu) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

func (m menu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

func menuProperties() map[string]dbus.Variant {
	v := dbus.MakeVariant
	return map[string]dbus.Variant{"Version": v(uint32(3)), "TextDirection": v("ltr"),
		"Status": v("normal"), "IconThemePath": v([]string{})}
}
