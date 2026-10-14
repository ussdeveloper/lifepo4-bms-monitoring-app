//go:build windows

package main

import (
	"testing"
	"time"
)

// fakeDrag simulates cursor input: every frame the mouse moves 10 px to the
// left and after four frames the button is released.
type fakeDrag struct {
	cursor     winPoint
	boundsRect winRect
	pressed    bool
	frames     int
	moves      []winPoint
	cursorAt   []winPoint
	restore    bool
	stepX      int32
	releaseAt  int
}

func (f *fakeDrag) environment() dragEnvironment {
	if f.stepX == 0 {
		f.stepX = -10
	}
	if f.releaseAt == 0 {
		f.releaseAt = 4
	}
	return dragEnvironment{
		buttonDown: func() bool { return f.pressed },
		cursor:     func() (winPoint, bool) { return f.cursor, true },
		bounds:     func() (winRect, bool) { return f.boundsRect, true },
		restore:    func() bool { return f.restore },
		move: func(x, y int32) {
			f.moves = append(f.moves, winPoint{x: x, y: y})
			f.cursorAt = append(f.cursorAt, f.cursor)
		},
		sleep: func(time.Duration) {
			f.frames++
			f.cursor.x += f.stepX
			if f.frames >= f.releaseAt {
				f.pressed = false
			}
		},
	}
}

func TestDragMovesWindowWithCursor(t *testing.T) {
	fake := &fakeDrag{
		cursor:     winPoint{x: 700, y: 340},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    true,
	}
	runDrag(fake.environment())

	if len(fake.moves) < 3 {
		t.Fatalf("expected at least 3 window moves, got %d", len(fake.moves))
	}
	width := int32(fake.boundsRect.right - fake.boundsRect.left)
	for index := range fake.moves {
		offsetX := fake.cursorAt[index].x - fake.moves[index].x
		offsetY := fake.cursorAt[index].y - fake.moves[index].y
		if offsetX < dragMinMargin || offsetX > width-dragMinMargin {
			t.Errorf("move %d: horizontal grab offset %d outside the window", index, offsetX)
		}
		if offsetY < 0 || offsetY > dragMaxOffsetY {
			t.Errorf("move %d: vertical grab offset %d outside the title bar", index, offsetY)
		}
	}
	// The window must follow the cursor one to one.
	for index := 1; index < len(fake.moves); index++ {
		if fake.moves[index].x-fake.moves[index-1].x != fake.cursorAt[index].x-fake.cursorAt[index-1].x {
			t.Errorf("move %d: the window did not follow the cursor horizontally", index)
		}
	}
}

func TestDragStopsWhenButtonReleased(t *testing.T) {
	fake := &fakeDrag{
		cursor:     winPoint{x: 900, y: 350},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    false,
	}
	runDrag(fake.environment())

	if len(fake.moves) != 0 {
		t.Fatalf("window must not move when the button is already released, got %v", fake.moves)
	}
}

func TestDragKeepsGrabPointInsideRestoredWindow(t *testing.T) {
	// Maximized window: the cursor is close to the right screen edge, so the
	// grab point would land far outside the restored window without clamping.
	fake := &fakeDrag{
		cursor:     winPoint{x: 2550, y: 700},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    true,
		restore:    true,
	}
	runDrag(fake.environment())

	if len(fake.moves) == 0 {
		t.Fatal("expected the window to move after restoring")
	}
	width := int32(fake.boundsRect.right - fake.boundsRect.left)
	for index := range fake.moves {
		offsetX := fake.cursorAt[index].x - fake.moves[index].x
		offsetY := fake.cursorAt[index].y - fake.moves[index].y
		if offsetX < dragMinMargin || offsetX > width-dragMinMargin {
			t.Errorf("move %d: horizontal grab offset %d outside the window", index, offsetX)
		}
		if offsetY < 0 || offsetY > dragMaxOffsetY {
			t.Errorf("move %d: vertical grab offset %d outside the title bar", index, offsetY)
		}
	}
}

func TestDragWithoutCursorIsIgnored(t *testing.T) {
	env := dragEnvironment{
		buttonDown: func() bool { return true },
		cursor:     func() (winPoint, bool) { return winPoint{}, false },
		bounds:     func() (winRect, bool) { return winRect{}, true },
		restore:    func() bool { return false },
		move:       func(int32, int32) { t.Error("the window must not move") },
		sleep:      func(time.Duration) {},
	}
	runDrag(env)
}

/* --- resizing (the web view covers the window edges, so the window is sized
   from here) --- */

// fakeResize moves the cursor 12 px per frame and releases after four frames.
type fakeResize struct {
	cursor     winPoint
	boundsRect winRect
	pressed    bool
	frames     int
	stepX      int32
	stepY      int32
	releaseAt  int
	rects      []winRect
}

func (f *fakeResize) environment() resizeEnvironment {
	if f.stepX == 0 {
		f.stepX = 12
	}
	if f.releaseAt == 0 {
		f.releaseAt = 4
	}
	return resizeEnvironment{
		buttonDown: func() bool { return f.pressed },
		cursor:     func() (winPoint, bool) { return f.cursor, true },
		bounds:     func() (winRect, bool) { return f.boundsRect, true },
		move: func(left, top, width, height int32) {
			f.rects = append(f.rects, winRect{left: left, top: top, right: left + width, bottom: top + height})
		},
		sleep: func(time.Duration) {
			f.frames++
			f.cursor.x += f.stepX
			f.cursor.y += f.stepY
			if f.frames >= f.releaseAt {
				f.pressed = false
			}
		},
	}
}

func TestResizeRightEdgeKeepsTheLeftEdge(t *testing.T) {
	fake := &fakeResize{
		cursor:     winPoint{x: 1920, y: 600},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    true,
	}
	runResize("right", fake.environment())

	if len(fake.rects) < 3 {
		t.Fatalf("expected several resize steps, got %d", len(fake.rects))
	}
	last := fake.rects[len(fake.rects)-1]
	if last.left != 640 || last.top != 320 {
		t.Errorf("the left and top edges must not move, got %+v", last)
	}
	if last.right != 1920+36 {
		t.Errorf("the right edge must follow the cursor, got %d", last.right)
	}
}

func TestResizeTopLeftEdgeMovesBothEdges(t *testing.T) {
	fake := &fakeResize{
		cursor:     winPoint{x: 640, y: 320},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    true,
		stepX:      12,
		stepY:      8,
	}
	runResize("topleft", fake.environment())

	last := fake.rects[len(fake.rects)-1]
	if last.left != 640+36 || last.top != 320+24 {
		t.Errorf("both edges must follow the cursor, got %+v", last)
	}
	if last.right != 1920 || last.bottom != 1120 {
		t.Errorf("the opposite edges must stay, got %+v", last)
	}
}

func TestResizeRespectsTheMinimumSize(t *testing.T) {
	// The cursor is dragged far beyond the opposite edge: the window must stop
	// at its minimum size instead of collapsing.
	fake := &fakeResize{
		cursor:     winPoint{x: 1920, y: 600},
		boundsRect: winRect{left: 640, top: 320, right: 1920, bottom: 1120},
		pressed:    true,
		stepX:      -400,
		releaseAt:  3,
	}
	runResize("right", fake.environment())

	last := fake.rects[len(fake.rects)-1]
	if last.right-last.left != minWindowWidth {
		t.Errorf("width must be clamped to %d, got %d", minWindowWidth, last.right-last.left)
	}
}

func TestResizeWithoutCursorIsIgnored(t *testing.T) {
	env := resizeEnvironment{
		buttonDown: func() bool { return true },
		cursor:     func() (winPoint, bool) { return winPoint{}, false },
		bounds:     func() (winRect, bool) { return winRect{}, true },
		move:       func(int32, int32, int32, int32) { t.Error("the window must not be resized") },
		sleep:      func(time.Duration) {},
	}
	runResize("bottomright", env)
}
