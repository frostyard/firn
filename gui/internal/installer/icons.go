package installer

import "github.com/egoist/mygo/ui"

// The installer's icons, drawn for it on a 24-unit grid with round strokes.
// ui.Icon paints them in the text color around them.

func icon(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	iconSnowflake = icon(`<line x1="12" y1="2" x2="12" y2="22"/><line x1="3.3" y1="7" x2="20.7" y2="17"/><line x1="3.3" y1="17" x2="20.7" y2="7"/>` +
		`<polyline points="9.5,3.5 12,6 14.5,3.5"/><polyline points="9.5,20.5 12,18 14.5,20.5"/>` +
		`<polyline points="3.2,10.3 6.6,9.4 5.7,6"/><polyline points="20.8,13.7 17.4,14.6 18.3,18"/>` +
		`<polyline points="5.7,18 6.6,14.6 3.2,13.7"/><polyline points="18.3,6 17.4,9.4 20.8,10.3"/>`)
	iconCheck      = icon(`<polyline points="5,12.5 10,17.5 19,7"/>`)
	iconArrowRight = icon(`<line x1="5" y1="12" x2="19" y2="12"/><polyline points="13,6 19,12 13,18"/>`)
	iconArrowLeft  = icon(`<line x1="19" y1="12" x2="5" y2="12"/><polyline points="11,6 5,12 11,18"/>`)
	iconHome       = icon(`<path d="M4 11 12 4l8 7"/><path d="M6 9.5V20h12V9.5"/><rect x="10" y="14" width="4" height="6"/>`)
	iconMonitor    = icon(`<rect x="3" y="4" width="18" height="12" rx="2"/><line x1="12" y1="16" x2="12" y2="20"/><line x1="8" y1="20" x2="16" y2="20"/>`)
	iconTablet     = icon(`<rect x="2.5" y="5" width="19" height="13" rx="2"/><line x1="2.5" y1="20" x2="21.5" y2="20"/><line x1="6" y1="8.5" x2="6" y2="14.5"/>`)
	iconServer     = icon(`<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><line x1="7" y1="7.5" x2="7.01" y2="7.5"/><line x1="7" y1="16.5" x2="7.01" y2="16.5"/><line x1="11" y1="7.5" x2="17" y2="7.5"/><line x1="11" y1="16.5" x2="17" y2="16.5"/>`)
	iconDrive      = icon(`<rect x="3" y="6" width="18" height="12" rx="2"/><line x1="3" y1="13" x2="21" y2="13"/><line x1="7" y1="15.5" x2="7.01" y2="15.5"/><line x1="10.5" y1="15.5" x2="10.51" y2="15.5"/>`)
	iconUSB        = icon(`<rect x="7" y="9" width="10" height="13" rx="2"/><path d="M9 9V3h6v6"/><line x1="11" y1="5.5" x2="11" y2="6.5"/><line x1="13" y1="5.5" x2="13" y2="6.5"/>`)
	iconLock       = icon(`<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7.5a4 4 0 0 1 8 0V11"/><line x1="12" y1="15" x2="12" y2="17"/>`)
	iconUnlock     = icon(`<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7.5a4 4 0 0 1 7.6-1.7"/>`)
	iconKey        = icon(`<circle cx="8" cy="15" r="4.5"/><line x1="11.2" y1="11.8" x2="20" y2="3"/><line x1="16.5" y1="6.5" x2="19" y2="9"/><line x1="14" y1="9" x2="16" y2="11"/>`)
	iconChip       = icon(`<rect x="6" y="6" width="12" height="12" rx="2"/><rect x="9.5" y="9.5" width="5" height="5" rx="1"/>` +
		`<line x1="9" y1="2.5" x2="9" y2="6"/><line x1="15" y1="2.5" x2="15" y2="6"/><line x1="9" y1="18" x2="9" y2="21.5"/><line x1="15" y1="18" x2="15" y2="21.5"/>` +
		`<line x1="2.5" y1="9" x2="6" y2="9"/><line x1="2.5" y1="15" x2="6" y2="15"/><line x1="18" y1="9" x2="21.5" y2="9"/><line x1="18" y1="15" x2="21.5" y2="15"/>`)
	iconShield   = icon(`<path d="M12 2.5 4 5.5v6c0 5 3.4 8.6 8 10 4.6-1.4 8-5 8-10v-6z"/><polyline points="8.5,12 11,14.5 15.5,9.5"/>`)
	iconGlobe    = icon(`<circle cx="12" cy="12" r="9"/><line x1="3" y1="12" x2="21" y2="12"/><path d="M12 3c2.6 2.6 3.8 5.6 3.8 9s-1.2 6.4-3.8 9c-2.6-2.6-3.8-5.6-3.8-9S9.4 5.6 12 3z"/>`)
	iconUser     = icon(`<circle cx="12" cy="8" r="4"/><path d="M4 21c.8-4.2 4-6.5 8-6.5s7.2 2.3 8 6.5"/>`)
	iconPackage  = icon(`<path d="M12 2.5 20.5 7v10L12 21.5 3.5 17V7z"/><polyline points="3.5,7 12,11.5 20.5,7"/><line x1="12" y1="11.5" x2="12" y2="21.5"/><line x1="7.8" y1="4.8" x2="16.2" y2="9.2"/>`)
	iconList     = icon(`<rect x="4" y="3" width="16" height="18" rx="2"/><polyline points="7.5,9 9,10.5 11.5,8"/><line x1="13.5" y1="9.2" x2="16.5" y2="9.2"/><polyline points="7.5,15 9,16.5 11.5,14"/><line x1="13.5" y1="15.2" x2="16.5" y2="15.2"/>`)
	iconDownload = icon(`<line x1="12" y1="3" x2="12" y2="15"/><polyline points="7,10 12,15 17,10"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/>`)
	iconWarning  = icon(`<path d="M12 3.5 2.5 20h19z"/><line x1="12" y1="10" x2="12" y2="14"/><line x1="12" y1="17" x2="12.01" y2="17"/>`)
	iconInfo     = icon(`<circle cx="12" cy="12" r="9"/><line x1="12" y1="11" x2="12" y2="16.5"/><line x1="12" y1="7.5" x2="12.01" y2="7.5"/>`)
	iconError    = icon(`<circle cx="12" cy="12" r="9"/><line x1="9" y1="9" x2="15" y2="15"/><line x1="15" y1="9" x2="9" y2="15"/>`)
	iconSuccess  = icon(`<circle cx="12" cy="12" r="9"/><polyline points="8,12.5 11,15.5 16.5,9.5"/>`)
	iconKeyboard = icon(`<rect x="2.5" y="6" width="19" height="12" rx="2"/><line x1="6" y1="9.5" x2="6.01" y2="9.5"/><line x1="9.5" y1="9.5" x2="9.51" y2="9.5"/><line x1="13" y1="9.5" x2="13.01" y2="9.5"/><line x1="16.5" y1="9.5" x2="16.51" y2="9.5"/><line x1="6" y1="12.5" x2="6.01" y2="12.5"/><line x1="18" y1="12.5" x2="18.01" y2="12.5"/><line x1="8" y1="15" x2="16" y2="15"/>`)
	iconRefresh  = icon(`<path d="M20 12a8 8 0 1 1-2.3-5.6"/><polyline points="20,4 20,9 15,9"/>`)
	iconSun      = icon(`<circle cx="12" cy="12" r="4"/><line x1="12" y1="2" x2="12" y2="4"/><line x1="12" y1="20" x2="12" y2="22"/><line x1="2" y1="12" x2="4" y2="12"/><line x1="20" y1="12" x2="22" y2="12"/><line x1="4.9" y1="4.9" x2="6.3" y2="6.3"/><line x1="17.7" y1="17.7" x2="19.1" y2="19.1"/><line x1="4.9" y1="19.1" x2="6.3" y2="17.7"/><line x1="17.7" y1="6.3" x2="19.1" y2="4.9"/>`)
	iconMoon     = icon(`<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>`)
	iconPower    = icon(`<line x1="12" y1="2.5" x2="12" y2="11"/><path d="M6.3 6.3a8 8 0 1 0 11.4 0"/>`)
	iconFile     = icon(`<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="14,3 14,9 20,9"/>`)
	iconSparkle  = icon(`<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6"/>`)
	iconEye      = icon(`<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="3"/>`)
	iconEyeOff   = icon(`<path d="M9.9 5.7A9.6 9.6 0 0 1 12 5.5C18 5.5 21.5 12 21.5 12a17 17 0 0 1-2.6 3.5M6.6 6.6C3.9 8.3 2.5 12 2.5 12S6 18.5 12 18.5a9.4 9.4 0 0 0 5.4-1.6"/><line x1="3" y1="3" x2="21" y2="21"/>`)
	iconPencil   = icon(`<path d="M15.5 4.5l4 4L8 20H4v-4z"/><line x1="13" y1="7" x2="17" y2="11"/>`)
)
