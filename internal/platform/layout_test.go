package platform

import "testing"

func TestTiledRectanglesFillWorkArea(t *testing.T) {
	got := TiledRectangles(3, Rect{Width: 1000, Height: 800})
	want := []Rect{{0, 0, 500, 400}, {500, 0, 500, 400}, {0, 400, 1000, 400}}
	if len(got) != len(want) {
		t.Fatalf("got=%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%#v", i, got[i])
		}
	}
}
