//go:build !(js && wasm)

package platform

// The sidebar's virtual window (railwin_wasm.go): no DOM to measure, so the
// view renders its first window, which is what its native tests assert on.
func RailWindow(scrollerSel string, pitch, step int) string { return "" }

func OnRailWindow(scrollerSel string, pitch, step int, fn func(spec string)) Listener {
	return Listener{}
}
