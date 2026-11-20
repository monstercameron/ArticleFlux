//go:build !(js && wasm)

package platform

// FocusIfLost (focuslost_wasm.go): no focus to lose.
func FocusIfLost(containerSelector, itemSelector string) {}

// ScrollSettingsIntoView (panelscroll_wasm.go): nothing to scroll.
func ScrollSettingsIntoView() {}
