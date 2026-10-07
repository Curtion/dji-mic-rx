package console

import "github.com/egoist/mygo/ui"

// The icon set is drawn in the style of Lucide: 24 by 24, strokes only, round
// caps, so it inherits the text colour and the weight of the text beside it.
// The receiver icon is drawn from the hardware itself — a puck with a
// connector and two transmitter lights.
const (
	svgReceiver = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="6.5" width="16" height="11" rx="3"/><path d="M18 10h3v4h-3"/><path d="M9.5 10.5h.01"/><path d="M14 10.5h.01"/><path d="M6 10.5h.01"/></svg>`
	svgSliders  = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h10"/><path d="M18 7h2"/><path d="M14 7h.01"/><circle cx="16" cy="7" r="1.8"/><path d="M4 12h4"/><circle cx="10" cy="12" r="1.8"/><path d="M12 12h8"/><path d="M4 17h12"/><circle cx="18" cy="17" r="1.8"/><path d="M20 17h.01"/></svg>`
	svgChip     = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="6" width="12" height="12" rx="2.5"/><path d="M9.5 3v3"/><path d="M14.5 3v3"/><path d="M9.5 18v3"/><path d="M14.5 18v3"/><path d="M3 9.5h3"/><path d="M3 14.5h3"/><path d="M18 9.5h3"/><path d="M18 14.5h3"/></svg>`
	svgInfo     = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v5.5"/><path d="M12 7.6h.01"/></svg>`
	svgBolt     = `<svg viewBox="0 0 24 24" fill="currentColor" stroke="none"><path d="M13.4 2 5 13.2h5.1L9.8 22l8.4-11.2h-5.1z"/></svg>`
	svgWarning  = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4.5 2.8 20h18.4z"/><path d="M12 10v4.2"/><path d="M12 17.4h.01"/></svg>`
	svgRefresh  = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5a8 8 0 1 0-2.4 6.3"/><path d="M20 4.5v7h-7"/></svg>`
	svgDownload = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/></svg>`
)

// The icons are parsed once, as the toolkit asks, never inside the view.
var (
	iconReceiver = ui.MustParseSVG([]byte(svgReceiver))
	iconSliders  = ui.MustParseSVG([]byte(svgSliders))
	iconChip     = ui.MustParseSVG([]byte(svgChip))
	iconInfo     = ui.MustParseSVG([]byte(svgInfo))
	iconBolt     = ui.MustParseSVG([]byte(svgBolt))
	iconWarning  = ui.MustParseSVG([]byte(svgWarning))
	iconRefresh  = ui.MustParseSVG([]byte(svgRefresh))
	iconDownload = ui.MustParseSVG([]byte(svgDownload))
)
