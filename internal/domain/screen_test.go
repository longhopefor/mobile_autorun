package domain

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"
)

func TestScreenContentComparison(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 50, 100))
	b := image.NewRGBA(a.Bounds())
	encode := func(m image.Image, level png.CompressionLevel) []byte {
		var buf bytes.Buffer
		e := (&png.Encoder{CompressionLevel: level}).Encode(&buf, m)
		if e != nil {
			t.Fatal(e)
		}
		return buf.Bytes()
	}
	original := encode(a, png.DefaultCompression)
	b.Set(10, 1, color.White)
	ok, e := SameScreen(original, encode(b, png.NoCompression))
	if e != nil || !ok {
		t.Fatal("status bar/encoding change rejected", e)
	}
	b.Set(10, 40, color.White)
	ok, e = SameScreen(original, encode(b, png.DefaultCompression))
	if e != nil || ok {
		t.Fatal("changed application content accepted", e)
	}
}

func TestInputTargetMustStayIdentical(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 50, 100))
	b := image.NewRGBA(a.Bounds())
	b.Set(10, 40, color.White)
	encode := func(m image.Image) []byte { var buf bytes.Buffer; _ = png.Encode(&buf, m); return buf.Bytes() }
	state := []byte(`{"a11y_tree":[{"text":"","resourceId":"search"}],"phone_state":{"isEditable":true,"focusedElement":{"resourceId":"search"},"packageName":"settings"}}`)
	before := Observation{Image: encode(a), State: state}
	after := Observation{Image: encode(b), State: state}
	ok, e := SameObservationForAction(before, after, Action{Kind: "type"})
	if e != nil || !ok {
		t.Fatal("caret with same target rejected", e)
	}
	after.State = []byte(`{"a11y_tree":[{"text":"changed","resourceId":"search"}],"phone_state":{"isEditable":true,"focusedElement":{"resourceId":"search"},"packageName":"settings"}}`)
	ok, e = SameObservationForAction(before, after, Action{Kind: "type"})
	if e != nil || ok {
		t.Fatal("changed field accepted", e)
	}
	ok, e = SameObservationForAction(before, Observation{Image: encode(b), State: state}, Action{Kind: "tap", Y: 500})
	if e != nil || ok {
		t.Fatal("coordinate action accepted changed pixels", e)
	}
}

func TestTapCaretBlink(t *testing.T) {
	encode := func(m image.Image) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, m); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	state := `{"a11y_tree":{"children":[{"resourceId":"title","isEditable":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":10,"top":50,"right":190,"bottom":100}},{"resourceId":"body","isEditable":true,"isFocused":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":0,"top":120,"right":200,"bottom":200}},{"resourceId":"search","text":"搜索","isClickable":true,"isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":20,"top":280,"right":180,"bottom":330}}]},"phone_state":{"isEditable":true,"focusedElement":{"resourceId":"body"},"packageName":"test.app","activityName":"Editor"}}`
	for _, tc := range []struct {
		name        string
		kind        string
		x, y        int
		stateChange func(string) string
		baseChange  func(string) string
		imageChange func(*image.RGBA)
		want        bool
	}{
		{name: "caret appears", want: true},
		{name: "caret disappears", want: true},
		{name: "caret tint over hint", want: true},
		{name: "caret tint over gray hint", want: true},
		{name: "caret alpha overlay over hint", want: true},
		{name: "button tap", x: 500, y: 800, want: true},
		{name: "unknown coordinate tap", x: 500, y: 900},
		{name: "disabled button", x: 500, y: 800, baseChange: func(s string) string {
			return strings.Replace(s, `"text":"搜索","isClickable":true,"isVisibleToUser":true,"isEnabled":true`, `"text":"搜索","isClickable":true,"isVisibleToUser":true,"isEnabled":false`, 1)
		}},
		{name: "hidden button", x: 500, y: 800, baseChange: func(s string) string {
			return strings.Replace(s, `"text":"搜索","isClickable":true,"isVisibleToUser":true`, `"text":"搜索","isClickable":true,"isVisibleToUser":false`, 1)
		}},
		{name: "noninteractive label", x: 500, y: 800, baseChange: func(s string) string {
			return strings.Replace(s, `"text":"搜索","isClickable":true`, `"text":"搜索","isClickable":false`, 1)
		}},
		{name: "unnamed button", x: 500, y: 800, want: true, baseChange: func(s string) string { return strings.Replace(s, `"resourceId":"search"`, `"resourceId":""`, 1) }},
		{name: "recycled identity elsewhere", x: 500, y: 800, want: true, baseChange: func(s string) string { return strings.Replace(s, `"resourceId":"title"`, `"resourceId":"search"`, 1) }},
		{name: "anonymous container with text child", x: 500, y: 780, want: true, baseChange: func(s string) string {
			return strings.Replace(s, `"resourceId":"search","text":"搜索"`, `"resourceId":"","text":"","children":[{"resourceId":"history_label","text":"ai 智能体 工作流","isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":40,"top":295,"right":160,"bottom":315}}]`, 1)
		}},
		{name: "anonymous container child changed", x: 500, y: 780, stateChange: func(s string) string {
			return strings.Replace(s, `"text":"ai 智能体 工作流"`, `"text":"其他词"`, 1)
		}, baseChange: func(s string) string {
			return strings.Replace(s, `"resourceId":"search","text":"搜索"`, `"resourceId":"","text":"","children":[{"resourceId":"history_label","text":"ai 智能体 工作流","isVisibleToUser":true,"isEnabled":true,"boundsInScreen":{"left":40,"top":295,"right":160,"bottom":315}}]`, 1)
		}},
		{name: "invalid target bounds", x: 500, y: 800, baseChange: func(s string) string {
			return strings.Replace(s, `"left":20,"top":280,"right":180,"bottom":330`, `"left":-1,"top":280,"right":201,"bottom":330`, 1)
		}},
		{name: "overlapping anonymous controls", x: 500, y: 800, baseChange: func(s string) string {
			s = strings.Replace(s, `"top":50,"right":190,"bottom":100`, `"top":280,"right":190,"bottom":330`, 1)
			return strings.ReplaceAll(s, `"resourceId":"search"`, `"resourceId":""`)
		}},
		{name: "overlapping targets", x: 500, y: 800, baseChange: func(s string) string {
			return strings.Replace(s, `"top":50,"right":190,"bottom":100`, `"top":280,"right":190,"bottom":330`, 1)
		}},
		{name: "button moved", x: 500, y: 800, stateChange: func(s string) string { return strings.Replace(s, `"top":280`, `"top":281`, 1) }},
		{name: "button label changed", x: 500, y: 800, stateChange: func(s string) string { return strings.Replace(s, `"text":"搜索"`, `"text":"取消"`, 1) }},
		{name: "button pixels changed", x: 500, y: 800, imageChange: func(m *image.RGBA) { m.Set(100, 300, color.Black) }},
		{name: "suggestions changed", x: 500, y: 800, imageChange: func(m *image.RGBA) { m.Set(100, 230, color.Black) }},
		{name: "foreground changed", x: 500, y: 800, stateChange: func(s string) string {
			return strings.Replace(s, `"activityName":"Editor"`, `"activityName":"Other"`, 1)
		}},
		{name: "status bar tap", x: 200, y: 20},
		{name: "swipe", kind: "swipe"},
		{name: "changed labels", stateChange: func(s string) string {
			return strings.Replace(s, `"resourceId":"title"`, `"text":"changed","resourceId":"title"`, 1)
		}},
		{name: "changed focus", stateChange: func(s string) string { return strings.ReplaceAll(s, `"resourceId":"body"`, `"resourceId":"other"`) }},
		{name: "changed layout", stateChange: func(s string) string { return strings.Replace(s, `"top":50`, `"top":51`, 1) }},
		{name: "other pixels changed", imageChange: func(m *image.RGBA) { m.Set(150, 250, color.Black) }},
		{name: "caret outside focus", imageChange: func(m *image.RGBA) {
			for y := 125; y < 137; y++ {
				for x := 8; x < 10; x++ {
					m.Set(x, y, color.White)
					m.Set(x, y+100, color.Black)
				}
			}
		}},
		{name: "wide animation", imageChange: func(m *image.RGBA) {
			for y := 125; y < 137; y++ {
				for x := 10; x < 20; x++ {
					m.Set(x, y, color.Black)
				}
			}
		}},
		{name: "nonuniform content", imageChange: func(m *image.RGBA) { m.Set(8, 125, color.Black) }},
		{name: "missing focused node", stateChange: func(s string) string { return strings.Replace(s, `"isFocused":true`, `"isFocused":false`, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := image.NewRGBA(image.Rect(0, 0, 200, 400))
			draw.Draw(a, a.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
			if tc.name == "caret tint over gray hint" {
				draw.Draw(a, image.Rect(0, 120, 200, 200), &image.Uniform{color.RGBA{R: 244, G: 244, B: 245, A: 255}}, image.Point{}, draw.Src)
			}
			if strings.Contains(tc.name, "over hint") || tc.name == "caret tint over gray hint" {
				for y := 125; y < 128; y++ {
					for x := 8; x < 10; x++ {
						a.Set(x, y, color.RGBA{R: 186, G: 186, B: 186, A: 255})
					}
				}
			}
			b := image.NewRGBA(a.Bounds())
			draw.Draw(b, b.Bounds(), a, image.Point{}, draw.Src)
			for y := 125; y < 137; y++ {
				for x := 8; x < 10; x++ {
					b.Set(x, y, color.RGBA{R: 255, A: 255})
				}
			}
			if strings.Contains(tc.name, "over hint") || tc.name == "caret tint over gray hint" {
				for y := 125; y < 137; y++ {
					for x := 8; x < 10; x++ {
						c := a.RGBAAt(x, y)
						if tc.name == "caret tint over gray hint" {
							b.Set(x, y, color.RGBA{R: uint8(min(255, int(c.R)*255/244)), G: uint8(int(c.G) * 36 / 244), B: uint8(int(c.B) * 66 / 245), A: 255})
						} else if tc.name == "caret tint over hint" {
							b.Set(x, y, color.RGBA{R: uint8(int(c.R) * 234 / 255), G: uint8(int(c.G) * 64 / 255), B: uint8(int(c.B) * 74 / 255), A: 255})
						} else {
							b.Set(x, y, color.RGBA{R: uint8((int(c.R) + 255) / 2), G: c.G / 2, B: c.B / 2, A: 255})
						}
					}
				}
			}
			if tc.imageChange != nil {
				tc.imageChange(b)
			}
			currentState := state
			if tc.baseChange != nil {
				currentState = tc.baseChange(state)
			}
			before := Observation{Width: 200, Height: 400, State: []byte(currentState), Image: encode(a)}
			after := Observation{Width: 200, Height: 400, State: []byte(currentState), Image: encode(b)}
			if tc.stateChange != nil {
				after.State = []byte(tc.stateChange(currentState))
			}
			if tc.name == "caret disappears" {
				before, after = after, before
			}
			kind := tc.kind
			if kind == "" {
				kind = "tap"
			}
			x, y := tc.x, tc.y
			if x == 0 && y == 0 {
				x, y = 200, 180
			}
			ok, err := SameObservationForAction(before, after, Action{Kind: kind, X: x, Y: y, Package: "test.app"})
			if err != nil || ok != tc.want {
				t.Fatalf("stable=%v err=%v want=%v", ok, err, tc.want)
			}
		})
	}
}
