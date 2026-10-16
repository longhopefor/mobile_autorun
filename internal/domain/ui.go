package domain

import (
	"bytes"
	"encoding/json"
	"errors"
)

type Bounds struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Right  int `json:"right"`
	Bottom int `json:"bottom"`
}
type UITarget struct {
	ResourceID  string `json:"resource_id,omitempty"`
	Text        string `json:"text,omitempty"`
	Description string `json:"description,omitempty"`
}
type UINode struct {
	ResourceID  string   `json:"resourceId"`
	Text        string   `json:"text"`
	Description string   `json:"contentDescription"`
	Hint        string   `json:"hint"`
	Class       string   `json:"className"`
	Bounds      Bounds   `json:"boundsInScreen"`
	Enabled     bool     `json:"isEnabled"`
	Visible     bool     `json:"isVisibleToUser"`
	Editable    bool     `json:"isEditable"`
	Focused     bool     `json:"isFocused"`
	Selected    bool     `json:"isSelected"`
	Clickable   bool     `json:"isClickable"`
	Children    []UINode `json:"children,omitempty"`
}
type UI struct {
	Package  string
	Activity string
	Nodes    []UINode
	Focus    string
	Keyboard bool
}

func ParseUI(o Observation) (UI, error) {
	var s struct {
		Tree  json.RawMessage `json:"a11y_tree"`
		Phone struct {
			Keyboard bool   `json:"keyboardVisible"`
			Package  string `json:"packageName"`
			Activity string `json:"activityName"`
			Focus    struct {
				ID string `json:"resourceId"`
			} `json:"focusedElement"`
		} `json:"phone_state"`
	}
	if e := json.Unmarshal(o.State, &s); e != nil {
		return UI{}, e
	}
	u := UI{Package: s.Phone.Package, Activity: s.Phone.Activity, Focus: s.Phone.Focus.ID, Keyboard: s.Phone.Keyboard}
	var roots []UINode
	if e := json.Unmarshal(s.Tree, &roots); e != nil {
		var root UINode
		if e = json.Unmarshal(s.Tree, &root); e != nil {
			return u, e
		}
		roots = []UINode{root}
	}
	var walk func(UINode)
	walk = func(n UINode) {
		children := n.Children
		n.Children = nil
		if len(u.Nodes) >= 8000 {
			return
		}
		u.Nodes = append(u.Nodes, n)
		for _, c := range children {
			walk(c)
		}
	}
	for _, n := range roots {
		walk(n)
	}
	return u, nil
}
func (u UI) Find(t UITarget) (UINode, error) {
	var found UINode
	count := 0
	for _, n := range u.Nodes {
		if !n.Visible || !n.Enabled || n.Bounds.Right <= n.Bounds.Left || n.Bounds.Bottom <= n.Bounds.Top {
			continue
		}
		if t.ResourceID != "" && t.ResourceID != n.ResourceID || t.Text != "" && t.Text != n.Text || t.Description != "" && t.Description != n.Description {
			continue
		}
		found = n
		count++
	}
	if count != 1 {
		return found, errors.New("ui_target_missing_or_ambiguous")
	}
	return found, nil
}
func (u UI) HasText(text string) bool {
	for _, n := range u.Nodes {
		if n.Visible && (n.Text == text || n.Description == text) {
			return true
		}
	}
	return false
}
func TargetTap(o Observation, t UITarget) (Action, error) {
	if o.Width < 2 || o.Height < 2 {
		return Action{}, errors.New("invalid_screen_dimensions")
	}
	u, e := ParseUI(o)
	if e != nil {
		return Action{}, e
	}
	n, e := u.Find(t)
	if e != nil {
		return Action{}, e
	}
	if n.Bounds.Left < 0 || n.Bounds.Top < 0 || n.Bounds.Right > o.Width || n.Bounds.Bottom > o.Height {
		return Action{}, errors.New("ui_target_outside_screen")
	}
	return Action{Kind: "tap", Package: u.Package, X: (n.Bounds.Left + n.Bounds.Right) * 500 / (o.Width - 1), Y: (n.Bounds.Top + n.Bounds.Bottom) * 500 / (o.Height - 1), Target: &t}, nil
}

// A named control may be used on an animated feed only if that exact control,
// its geometry and foreground remain identical in both observations.
func SameUITarget(a, b Observation, action Action) bool {
	if action.Target == nil || action.Kind != "tap" {
		return false
	}
	x, e := ParseUI(a)
	if e != nil {
		return false
	}
	y, e := ParseUI(b)
	if e != nil || x.Package != y.Package || x.Activity != y.Activity {
		return false
	}
	n, e := x.Find(*action.Target)
	if e != nil {
		return false
	}
	m, e := y.Find(*action.Target)
	if e != nil {
		return false
	}
	if a.Width < 2 || a.Height < 2 || a.Width != b.Width || a.Height != b.Height {
		return false
	}
	nb, _ := json.Marshal(n)
	mb, _ := json.Marshal(m)
	return bytes.Equal(nb, mb) && action.X == (n.Bounds.Left+n.Bounds.Right)*500/(a.Width-1) && action.Y == (n.Bounds.Top+n.Bounds.Bottom)*500/(a.Height-1)
}
